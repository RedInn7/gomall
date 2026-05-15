package cache

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// 库存在 Redis 里分两个桶维护:
//   stock:available:{pid}   还能下单的数量
//   stock:reserved:{pid}    已下单但未支付的数量 (占着但未真正扣)
// product.Num 在 DB 是初始水位；启动时由 syncer 把它复制到 Redis available

func StockAvailableKey(productID uint) string {
	return fmt.Sprintf("stock:available:%d", productID)
}

func StockReservedKey(productID uint) string {
	return fmt.Sprintf("stock:reserved:%d", productID)
}

var (
	ErrStockInsufficient = errors.New("库存不足")
	ErrStockNotInit      = errors.New("库存未初始化")
)

// reserveScript: 检查 available >= n，然后 available -= n, reserved += n
// KEYS[1]=available, KEYS[2]=reserved, ARGV[1]=n
// 返回 1=成功 / -1=不足 / -2=available 不存在 (商品库存未初始化)
var reserveScript = redis.NewScript(`
local avail = redis.call('GET', KEYS[1])
if avail == false then
    return -2
end
local need = tonumber(ARGV[1])
if tonumber(avail) < need then
    return -1
end
redis.call('DECRBY', KEYS[1], need)
redis.call('INCRBY', KEYS[2], need)
return 1
`)

// commitScript: 支付成功路径，把 reserved 桶里的减掉，available 不动
// 返回 1=成功 / -1=reserved 不足 (异常)
var commitScript = redis.NewScript(`
local r = redis.call('GET', KEYS[1])
if r == false or tonumber(r) < tonumber(ARGV[1]) then
    return -1
end
redis.call('DECRBY', KEYS[1], ARGV[1])
return 1
`)

// releaseScript: 订单取消路径，reserved 退回 available
var releaseScript = redis.NewScript(`
local r = redis.call('GET', KEYS[1])
if r == false or tonumber(r) < tonumber(ARGV[1]) then
    return -1
end
redis.call('DECRBY', KEYS[1], ARGV[1])
redis.call('INCRBY', KEYS[2], ARGV[1])
return 1
`)

// InitStock 把商品库存水位写入 available 桶（覆盖写），reserved 初始 0
func InitStock(ctx context.Context, productID uint, available int64) error {
	pipe := RedisClient.TxPipeline()
	pipe.Set(ctx, StockAvailableKey(productID), available, 0)
	pipe.Set(ctx, StockReservedKey(productID), 0, 0)
	_, err := pipe.Exec(ctx)
	return err
}

// ReserveStock 预扣 n 件 (available -> reserved)
func ReserveStock(ctx context.Context, productID uint, n int64) error {
	res, err := reserveScript.Run(ctx, RedisClient,
		[]string{StockAvailableKey(productID), StockReservedKey(productID)},
		n).Result()
	if err != nil {
		return err
	}
	code, _ := res.(int64)
	switch code {
	case 1:
		return nil
	case -1:
		return ErrStockInsufficient
	case -2:
		return ErrStockNotInit
	}
	return fmt.Errorf("reserve script unknown return: %v", code)
}

// CommitReservation 支付成功，把 reserved 桶里减掉 (库存真正消耗)
func CommitReservation(ctx context.Context, productID uint, n int64) error {
	res, err := commitScript.Run(ctx, RedisClient,
		[]string{StockReservedKey(productID)},
		n).Result()
	if err != nil {
		return err
	}
	code, _ := res.(int64)
	if code != 1 {
		return fmt.Errorf("commit reservation failed (product=%d, n=%d): code=%d", productID, n, code)
	}
	return nil
}

// ReleaseReservation 取消/超时：reserved -> available
func ReleaseReservation(ctx context.Context, productID uint, n int64) error {
	res, err := releaseScript.Run(ctx, RedisClient,
		[]string{StockReservedKey(productID), StockAvailableKey(productID)},
		n).Result()
	if err != nil {
		return err
	}
	code, _ := res.(int64)
	if code != 1 {
		return fmt.Errorf("release reservation failed (product=%d, n=%d): code=%d", productID, n, code)
	}
	return nil
}

// GetStockSnapshot 读两个桶的当前值，用于巡检/对账
func GetStockSnapshot(ctx context.Context, productID uint) (available, reserved int64, err error) {
	a, e1 := RedisClient.Get(ctx, StockAvailableKey(productID)).Int64()
	if e1 != nil && e1 != redis.Nil {
		return 0, 0, e1
	}
	r, e2 := RedisClient.Get(ctx, StockReservedKey(productID)).Int64()
	if e2 != nil && e2 != redis.Nil {
		return 0, 0, e2
	}
	return a, r, nil
}
