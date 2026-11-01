package cache

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// 红包在 Redis 的存储结构：
//   redpacket:{id}:amounts   LIST  发包时一次性 RPUSH 预拆好的金额数组，LPOP 即抢
//   redpacket:{id}:claimed   HASH  userID -> "抢到金额:抢到时间(unix 秒)[:r]" (兜底单用户去重 + 对账找孤儿；
//                                  :r 表示对账任务已接手补领，Saga 回滚不再放回这一份)
//
// 抢红包 Lua 一次原子完成：HEXISTS 去重 -> LPOP -> HSET 标记，
// 不依赖 WATCH/MULTI，多副本/重连场景下也不会超发或重复发放。

var (
	ErrRedPacketDrained      = errors.New("红包已抢完")
	ErrRedPacketAlreadyTaken = errors.New("已领取过本红包")
	ErrRedPacketSplitInvalid = errors.New("红包金额或份数非法")
)

// prepareRedPacketScript 一次性把金额数组 RPUSH 入 list，并设置过期
//
//	KEYS[1] = amounts list key
//	ARGV[1..n-1] = 各份金额 (string)
//	ARGV[n]      = ttl 秒
//	返回写入的份数
var prepareRedPacketScript = redis.NewScript(`
local n = #ARGV
if n < 2 then return 0 end
local ttl = tonumber(ARGV[n])
for i = 1, n - 1 do
    redis.call('RPUSH', KEYS[1], ARGV[i])
end
redis.call('EXPIRE', KEYS[1], ttl)
return n - 1
`)

// claimRedPacketScript 抢一份红包
//
//	KEYS[1] = amounts list key
//	KEYS[2] = claimed hash key
//	ARGV[1] = userID (string)
//	ARGV[2] = claimed hash TTL (秒)
//	ARGV[3] = 当前 unix 秒，记进标记，供对账跳过还在途的领取
//	返回 >0 抢到金额；-1 已抢完；-2 已领过
var claimRedPacketScript = redis.NewScript(`
if redis.call('HEXISTS', KEYS[2], ARGV[1]) == 1 then
    return -2
end
local amt = redis.call('LPOP', KEYS[1])
if not amt then
    return -1
end
redis.call('HSET', KEYS[2], ARGV[1], amt .. ':' .. ARGV[3])
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[2]))
return tonumber(amt)
`)

// releaseRedPacketLeftScript 过期回收：把 list 里剩余金额加总返回 + 清 list
//
//	KEYS[1] = amounts list key
//	返回剩余总金额
var releaseRedPacketLeftScript = redis.NewScript(`
local sum = 0
while true do
    local amt = redis.call('LPOP', KEYS[1])
    if not amt then break end
    sum = sum + tonumber(amt)
end
return sum
`)

// rollbackRedPacketClaimScript Saga 回滚：DB 写失败时把这一份金额 LPUSH 回 list、
// 并从 claimed hash 删除用户标记 (允许其下次再抢)。
// 先比较再删：标记已被对账任务改成「补领中」(后缀 :r) 时不动，否则对账补领入账后这里再放回一份，
// Redis 比 DB 多一份，最后有人被拒、发出的总额偏离托管。标记已不存在（已回补过）也不放回。
//
//	KEYS[1] = amounts list key
//	KEYS[2] = claimed hash key
//	ARGV[1] = userID
//	ARGV[2] = amount
//	返回 1 已回补；0 标记不存在；-1 对账补领中
var rollbackRedPacketClaimScript = redis.NewScript(`
local v = redis.call('HGET', KEYS[2], ARGV[1])
if not v then return 0 end
if string.sub(v, -2) == ':r' then return -1 end
redis.call('LPUSH', KEYS[1], ARGV[2])
redis.call('HDEL', KEYS[2], ARGV[1])
return 1
`)

// lockRedPacketClaimForRepairScript 对账补领前把标记 CAS 成「补领中」，和 Saga 回滚互斥：
// 谁先改到标记谁处理这一份，另一方让路。
//
//	KEYS[1] = claimed hash key
//	ARGV[1] = userID
//	ARGV[2] = 对账读到的原标记值
//	返回 1 拿到；0 标记已变（被回补 / 重抢）
var lockRedPacketClaimForRepairScript = redis.NewScript(`
local v = redis.call('HGET', KEYS[1], ARGV[1])
if v ~= ARGV[2] then return 0 end
if string.sub(v, -2) ~= ':r' then
    redis.call('HSET', KEYS[1], ARGV[1], v .. ':r')
end
return 1
`)

// splitRand 进程内独立 rand 源 (math/rand 全局源加锁，热点路径并发不友好)
var splitRand = struct {
	sync.Mutex
	r *rand.Rand
}{r: rand.New(rand.NewSource(time.Now().UnixNano()))}

// SplitRedPacket 二倍均值法拆分红包：每次随机 [1, 2*avg-1] 分，最后一份兜底
//   - 总和严格等于 total
//   - 每份最少 1 分
//   - 末尾 shuffle 避免顺序敏感 (排序后看分布更均匀)
func SplitRedPacket(total int64, count int) ([]int64, error) {
	if count <= 0 || total < int64(count) {
		return nil, ErrRedPacketSplitInvalid
	}

	out := make([]int64, count)
	remain := total

	splitRand.Lock()
	defer splitRand.Unlock()

	for i := 0; i < count-1; i++ {
		left := int64(count - i)
		// 二倍均值：[1, 2*avg-1]，avg = remain / left
		avg := remain / left * 2
		if avg < 2 {
			avg = 2
		}
		// 保留下游 (count-1-i) 份每份至少 1 分
		maxThisRound := remain - int64(count-1-i)
		if maxThisRound < 1 {
			maxThisRound = 1
		}
		hi := avg - 1
		if hi > maxThisRound {
			hi = maxThisRound
		}
		if hi < 1 {
			hi = 1
		}
		amt := splitRand.r.Int63n(hi) + 1
		out[i] = amt
		remain -= amt
	}
	out[count-1] = remain

	splitRand.r.Shuffle(count, func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out, nil
}

// PrepareRedPacket 把预拆金额数组写入 Redis list，TTL = expire - now (留 1h 余量给回收)
func PrepareRedPacket(ctx context.Context, id uint, amounts []int64, ttl time.Duration) error {
	if len(amounts) == 0 {
		return ErrRedPacketSplitInvalid
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	argv := make([]any, 0, len(amounts)+1)
	for _, a := range amounts {
		argv = append(argv, strconv.FormatInt(a, 10))
	}
	argv = append(argv, int(ttl.Seconds()))

	_, err := prepareRedPacketScript.Run(ctx, RedisClient,
		[]string{RedPacketAmountsKey(id)},
		argv...,
	).Result()
	return err
}

// ClaimRedPacket 原子抢一份；返回抢到金额或 (ErrRedPacketDrained / ErrRedPacketAlreadyTaken)
//
//	claimedTTL: claimed hash 的存活时间，一般设到红包过期之后再多留一段对账窗口
func ClaimRedPacket(ctx context.Context, id, userID uint, claimedTTL time.Duration) (int64, error) {
	if claimedTTL <= 0 {
		claimedTTL = 24 * time.Hour
	}
	res, err := claimRedPacketScript.Run(ctx, RedisClient,
		[]string{RedPacketAmountsKey(id), RedPacketClaimedKey(id)},
		strconv.FormatUint(uint64(userID), 10),
		int(claimedTTL.Seconds()),
		time.Now().Unix(),
	).Result()
	if err != nil {
		return 0, err
	}
	code, _ := res.(int64)
	switch {
	case code > 0:
		return code, nil
	case code == -1:
		return 0, ErrRedPacketDrained
	case code == -2:
		return 0, ErrRedPacketAlreadyTaken
	}
	return 0, fmt.Errorf("redpacket lua 未知返回 %v", code)
}

// ReleaseRedPacketLeft 过期回收：返回 list 剩余总金额并清空 list
func ReleaseRedPacketLeft(ctx context.Context, id uint) (int64, error) {
	res, err := releaseRedPacketLeftScript.Run(ctx, RedisClient,
		[]string{RedPacketAmountsKey(id)},
	).Result()
	if err != nil {
		return 0, err
	}
	sum, _ := res.(int64)
	return sum, nil
}

// ErrRedPacketClaimRepairing 回滚时发现对账任务已接手这一份，份额没放回。
var ErrRedPacketClaimRepairing = errors.New("红包领取正由对账补领")

// RollbackRedPacketClaim Saga 回滚：把一份已被 LPOP 的金额放回，并撤销 claimed 标记。
// 对账已接手时返回 ErrRedPacketClaimRepairing，调用方应回 DB 确认结果。
func RollbackRedPacketClaim(ctx context.Context, id, userID uint, amount int64) error {
	res, err := rollbackRedPacketClaimScript.Run(ctx, RedisClient,
		[]string{RedPacketAmountsKey(id), RedPacketClaimedKey(id)},
		strconv.FormatUint(uint64(userID), 10),
		strconv.FormatInt(amount, 10),
	).Int64()
	if err != nil {
		return err
	}
	if res == -1 {
		return ErrRedPacketClaimRepairing
	}
	return nil
}

// LockRedPacketClaimForRepair 对账补领前抢占标记；raw 是 ListRedPacketClaimed 读到的原值。
// 返回 false 表示标记已被 Saga 回滚撤掉或换成了新的领取，这一份不该再补。
func LockRedPacketClaimForRepair(ctx context.Context, id, userID uint, raw string) (bool, error) {
	n, err := lockRedPacketClaimForRepairScript.Run(ctx, RedisClient,
		[]string{RedPacketClaimedKey(id)},
		strconv.FormatUint(uint64(userID), 10),
		raw,
	).Int64()
	return n == 1, err
}

// RedPacketClaimMark Redis claimed HASH 里的一条领取标记
type RedPacketClaimMark struct {
	Amount int64
	At     time.Time
	Raw    string // 原始标记值，LockRedPacketClaimForRepair 比较用
}

// ListRedPacketClaimed 读出红包全部领取标记，供对账和 DB 领取记录比对。
// 老格式（只有金额、没有时间）的 At 记为零值，视为早已过了在途窗口。
func ListRedPacketClaimed(ctx context.Context, id uint) (map[uint]RedPacketClaimMark, error) {
	raw, err := RedisClient.HGetAll(ctx, RedPacketClaimedKey(id)).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[uint]RedPacketClaimMark, len(raw))
	for k, v := range raw {
		uid, err := strconv.ParseUint(k, 10, 64)
		if err != nil {
			continue
		}
		amtStr, rest, _ := strings.Cut(v, ":")
		tsStr, _, _ := strings.Cut(rest, ":")
		amt, err := strconv.ParseInt(amtStr, 10, 64)
		if err != nil || amt <= 0 {
			continue
		}
		var at time.Time
		if ts, err := strconv.ParseInt(tsStr, 10, 64); err == nil {
			at = time.Unix(ts, 0)
		}
		out[uint(uid)] = RedPacketClaimMark{Amount: amt, At: at, Raw: v}
	}
	return out, nil
}

// RedPacketPoolExists 份额 LIST 是否还在（LIST 被抢空、过期、丢失或根本没写进去时都返回 false）
func RedPacketPoolExists(ctx context.Context, id uint) (bool, error) {
	n, err := RedisClient.Exists(ctx, RedPacketAmountsKey(id)).Result()
	return n > 0, err
}
