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

// 领券 Lua 脚本：原子检查 → 扣库存 → 标记用户已领
//
// KEYS[1] = stock key
// KEYS[2] = user-claimed key
// ARGV[1] = 用户最大可领数量
// ARGV[2] = 用户已领标记 TTL（秒）
//
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
		[]string{CouponStockKey(batchId), CouponUserClaimedKey(userId, batchId)},
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

// RollbackCouponStock 落库失败时回滚库存
func RollbackCouponStock(ctx context.Context, userId, batchId uint) {
	RedisClient.Incr(ctx, CouponStockKey(batchId))
	RedisClient.Decr(ctx, CouponUserClaimedKey(userId, batchId))
}
