package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// CouponStockKey 优惠券剩余库存
func CouponStockKey(batchId uint) string {
	return fmt.Sprintf("coupon:batch:%d:stock", batchId)
}

// CouponUserClaimedKey 用户已领标记
func CouponUserClaimedKey(userId, batchId uint) string {
	return fmt.Sprintf("coupon:user:%d:batch:%d", userId, batchId)
}

// CouponRollbackMarkKey 单次领取的回滚幂等标记，保证同一 (user,batch) 的回滚只生效一次。
func CouponRollbackMarkKey(userId, batchId uint) string {
	return fmt.Sprintf("coupon:rollback:%d:batch:%d", userId, batchId)
}

// couponRollbackMarkTTL 回滚幂等标记 TTL，覆盖落库失败到补偿的窗口即可，远大于业务重试间隔。
const couponRollbackMarkTTL = time.Hour

// 领券 Lua 脚本：原子检查 → 扣库存 → 标记用户已领
//
// KEYS[1] = stock key
// KEYS[2] = user-claimed key
// KEYS[3] = rollback mark key
// ARGV[1] = 用户最大可领数量
// ARGV[2] = 用户已领标记 TTL（秒）
//
// 成功领取时清掉上一次的回滚幂等标记，使本次新领取在失败时仍可被回滚一次。
// 返回 1 = 成功；-1 = 已抢光；-2 = 已超出单人上限；-3 = 库存 key 不存在（预热失败 / 被淘汰 / Redis 丢数据）
var claimScript = redis.NewScript(`
local stock = tonumber(redis.call('GET', KEYS[1]))
if stock == nil then
    return -3
end
if stock <= 0 then
    return -1
end
local owned = tonumber(redis.call('GET', KEYS[2]))
if owned == nil then owned = 0 end
if owned >= tonumber(ARGV[1]) then
    return -2
end
redis.call('DECR', KEYS[1])
redis.call('INCR', KEYS[2])
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[2]))
redis.call('DEL', KEYS[3])
return 1
`)

// 回滚 Lua 脚本：原子归还库存 + 撤销用户已领标记，并用幂等标记保证只生效一次。
//
// KEYS[1] = stock key
// KEYS[2] = user-claimed key
// KEYS[3] = rollback mark key（per-claim 幂等标记）
// ARGV[1] = 幂等标记 TTL（秒）
//
// 仅当标记不存在（SET NX 成功）时才执行补偿；重复回滚直接返回 0，
// 避免裸 INCR/DECR 双重补偿造成库存超额归还。
// 返回 1 = 本次执行了回滚；0 = 已回滚过，跳过。
var rollbackScript = redis.NewScript(`
if redis.call('SET', KEYS[3], '1', 'NX', 'EX', tonumber(ARGV[1])) == false then
    return 0
end
redis.call('INCR', KEYS[1])
redis.call('DECR', KEYS[2])
return 1
`)

// 售罄回滚脚本：DB 已发满时撤销用户已领标记，并把库存直接压到 0（不 INCR 归还）。
// 对账 / 重新装载可能把正在落库的领取算重，让 Redis 比真实剩余偏多；
// 偏多的那几个一旦被 DB 守卫拒掉就在这里清零，Redis 闸门恢复拦截。
// key 不存在时不创建，留给下次领取按 DB 重新装载。
//
// KEYS/ARGV 同回滚脚本。返回 1 = 执行了；0 = 已回滚过，跳过。
var soldOutScript = redis.NewScript(`
if redis.call('SET', KEYS[3], '1', 'NX', 'EX', tonumber(ARGV[1])) == false then
    return 0
end
redis.call('DECR', KEYS[2])
local ttl = redis.call('PTTL', KEYS[1])
if ttl > 0 then
    redis.call('SET', KEYS[1], 0, 'PX', ttl)
elseif ttl == -1 then
    redis.call('SET', KEYS[1], 0)
end
return 1
`)

// 抬高库存脚本：key 不存在或低于 DB 口径的剩余量时才写成剩余量，只升不降。
//
// KEYS[1] = stock key
// ARGV[1] = DB 口径剩余量（total - claimed）
// ARGV[2] = TTL（秒）
//
// 返回 1 = 写入了；0 = Redis 已不低于 DB 口径，未动。
var raiseStockScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]))
local want = tonumber(ARGV[1])
if cur ~= nil and cur >= want then
    return 0
end
redis.call('SET', KEYS[1], want, 'EX', tonumber(ARGV[2]))
return 1
`)

var (
	ErrCouponSoldOut       = errors.New("已抢光")
	ErrCouponPerUserExceed = errors.New("超出单人领取上限")
	// ErrCouponStockMissing Redis 里没有这个批次的库存 key，需要按 DB 重新装载，不能当"已抢光"。
	ErrCouponStockMissing = errors.New("coupon stock key missing")
)

// InitCouponStock 初始化库存（覆盖写）
func InitCouponStock(ctx context.Context, batchId uint, total int64, ttl time.Duration) error {
	return RedisClient.Set(ctx, CouponStockKey(batchId), total, ttl).Err()
}

// ClaimCouponAtomic 原子领券。返回是否成功 + 错误类型
func ClaimCouponAtomic(ctx context.Context, userId, batchId uint, perUser int64) (bool, error) {
	res, err := claimScript.Run(ctx, RedisClient,
		[]string{
			CouponStockKey(batchId),
			CouponUserClaimedKey(userId, batchId),
			CouponRollbackMarkKey(userId, batchId),
		},
		perUser, int((24 * time.Hour).Seconds()),
	).Result()
	if err != nil {
		return false, err
	}
	code, _ := res.(int64)
	switch code {
	case 1:
		return true, nil
	case -1:
		return false, ErrCouponSoldOut
	case -2:
		return false, ErrCouponPerUserExceed
	case -3:
		return false, ErrCouponStockMissing
	}
	return false, fmt.Errorf("coupon lua 未知返回 %v", code)
}

// RollbackCouponStock 落库失败时回滚库存。库存归还与用户已领标记撤销
// 经单条 Lua 脚本原子执行，保证两个计数同进同退；返回 error 供调用方观测与补偿。
// 通过 per-claim 幂等标记保证同一 (user,batch) 的回滚只生效一次，
// 即便调用方重试补偿也不会超额归还库存。
func RollbackCouponStock(ctx context.Context, userId, batchId uint) error {
	return rollbackScript.Run(ctx, RedisClient,
		[]string{
			CouponStockKey(batchId),
			CouponUserClaimedKey(userId, batchId),
			CouponRollbackMarkKey(userId, batchId),
		},
		int(couponRollbackMarkTTL.Seconds()),
	).Err()
}

// MarkCouponSoldOut 落库时 DB 已发满：撤销用户已领标记并把库存清零，而不是归还库存。
func MarkCouponSoldOut(ctx context.Context, userId, batchId uint) error {
	return soldOutScript.Run(ctx, RedisClient,
		[]string{
			CouponStockKey(batchId),
			CouponUserClaimedKey(userId, batchId),
			CouponRollbackMarkKey(userId, batchId),
		},
		int(couponRollbackMarkTTL.Seconds()),
	).Err()
}

// RaiseCouponStock 把 Redis 库存抬到 DB 口径的剩余量（只升不降），返回是否写入。
// 用于 key 丢失后的重新装载，以及落库失败回滚也失败、库存被"幽灵消耗"后的对账补偿。
// 低于 DB 口径的那部分可能是正在落库的领取，抬高后 Redis 会暂时偏多：超发由 PersistClaim 的 DB 守卫拦住，
// 被拦住的那次领取走 MarkCouponSoldOut 把库存清零，不会一直偏多。
func RaiseCouponStock(ctx context.Context, batchId uint, remaining int64, ttl time.Duration) (bool, error) {
	if remaining < 0 {
		remaining = 0
	}
	res, err := raiseStockScript.Run(ctx, RedisClient,
		[]string{CouponStockKey(batchId)}, remaining, int(ttl.Seconds())).Int()
	return res == 1, err
}
