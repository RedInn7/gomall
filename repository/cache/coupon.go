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
// 返回 1 = 成功；-1 = 已抢光；-2 = 已超出单人上限
var claimScript = redis.NewScript(`
local stock = tonumber(redis.call('GET', KEYS[1]))
if stock == nil or stock <= 0 then
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

var (
	ErrCouponSoldOut       = errors.New("已抢光")
	ErrCouponPerUserExceed = errors.New("超出单人领取上限")
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
