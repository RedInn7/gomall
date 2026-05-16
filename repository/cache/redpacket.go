package cache

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// 红包在 Redis 的存储结构：
//   redpacket:{id}:amounts   LIST  发包时一次性 RPUSH 预拆好的金额数组，LPOP 即抢
//   redpacket:{id}:claimed   HASH  userID -> 抢到金额 (兜底单用户去重)
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
//	返回 >0 抢到金额；-1 已抢完；-2 已领过
var claimRedPacketScript = redis.NewScript(`
if redis.call('HEXISTS', KEYS[2], ARGV[1]) == 1 then
    return -2
end
local amt = redis.call('LPOP', KEYS[1])
if not amt then
    return -1
end
redis.call('HSET', KEYS[2], ARGV[1], amt)
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
// 并从 claimed hash 删除用户标记 (允许其下次再抢)
//
//	KEYS[1] = amounts list key
//	KEYS[2] = claimed hash key
//	ARGV[1] = userID
//	ARGV[2] = amount
var rollbackRedPacketClaimScript = redis.NewScript(`
redis.call('LPUSH', KEYS[1], ARGV[2])
redis.call('HDEL', KEYS[2], ARGV[1])
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

// RollbackRedPacketClaim Saga 回滚：把一份已被 LPOP 的金额放回，并撤销 claimed 标记
func RollbackRedPacketClaim(ctx context.Context, id, userID uint, amount int64) error {
	_, err := rollbackRedPacketClaimScript.Run(ctx, RedisClient,
		[]string{RedPacketAmountsKey(id), RedPacketClaimedKey(id)},
		strconv.FormatUint(uint64(userID), 10),
		strconv.FormatInt(amount, 10),
	).Result()
	return err
}

// GetRedPacketRemainingCount Redis 视角下还剩多少份
func GetRedPacketRemainingCount(ctx context.Context, id uint) (int64, error) {
	n, err := RedisClient.LLen(ctx, RedPacketAmountsKey(id)).Result()
	if err != nil && err != redis.Nil {
		return 0, err
	}
	return n, nil
}
