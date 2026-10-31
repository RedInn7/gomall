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
// product.Num 在 DB 是剩余库存（付款才扣）；建商品时写入 available，启动预热和下单时
// 按 DB 补建缺失的桶，商家改库存时按差值调整 available

func StockAvailableKey(productID uint) string {
	return fmt.Sprintf("stock:available:%d", productID)
}

func StockReservedKey(productID uint) string {
	return fmt.Sprintf("stock:reserved:%d", productID)
}

var (
	ErrStockInsufficient = errors.New("库存不足")
	ErrStockNotInit      = errors.New("库存未初始化")
	// ErrReservationShort reserved 桶不够核销：这份预占已经不在 reserved 里，重试也核销不掉。
	ErrReservationShort = errors.New("预占不足，无法核销")
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

// initIfAbsentScript: available 不存在时按 DB 库存建桶，已存在则不动（返回 0）。
// DB 的 num 里还算着已预占、没付款的件数，所以 available = num - reserved（最低 0），
// reserved 不存在按 0 算。判断和写入在同一段 Lua 里，并发的懒初始化 / 启动预热不会互相覆盖。
// KEYS[1]=available, KEYS[2]=reserved, ARGV[1]=DB product.num
var initIfAbsentScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
    return 0
end
local avail = tonumber(ARGV[1]) - tonumber(redis.call('GET', KEYS[2]) or '0')
if avail < 0 then
    avail = 0
end
redis.call('SET', KEYS[1], avail)
return 1
`)

// adjustAvailableScript: 商家改库存后按差值调整 available。
// 静止时 available + reserved == DB num；商家把 num 从 old 改成 new，已预占的 reserved 不变，
// available 跟着变 delta = new - old，即 available = available + delta，最低为 0
// （new 比已预占的件数还少时先停卖，多出来的预占付款时由 DB 条件扣减拦住）。
// 不直接写 new - reserved：付款事务已扣 DB、还没扣 reserved 时改库存，这笔会被重复扣掉，差值不会。
// available 不存在时不动（返回 -1），下单时会按 DB 的新库存懒建桶。
// KEYS[1]=available, ARGV[1]=delta
var adjustAvailableScript = redis.NewScript(`
local avail = redis.call('GET', KEYS[1])
if avail == false then
    return -1
end
local target = tonumber(avail) + tonumber(ARGV[1])
if target < 0 then
    target = 0
end
redis.call('SET', KEYS[1], target)
return target
`)

// restockScript: 退款回补，available += n；available 不存在（未初始化）时不建桶
var restockScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
    redis.call('INCRBY', KEYS[1], ARGV[1])
end
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

// InitStockIfAbsent 按 DB 库存建 available 桶；桶已存在时不覆盖，不会抹掉运行期的预占。
func InitStockIfAbsent(ctx context.Context, productID uint, num int64) error {
	return initIfAbsentScript.Run(ctx, RedisClient,
		[]string{StockAvailableKey(productID), StockReservedKey(productID)}, num).Err()
}

// AdjustAvailable 商家把 DB 库存改了 delta 件后，同步调整 available，算法见 adjustAvailableScript。
func AdjustAvailable(ctx context.Context, productID uint, delta int64) error {
	return adjustAvailableScript.Run(ctx, RedisClient, []string{StockAvailableKey(productID)}, delta).Err()
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
		return fmt.Errorf("%w (product=%d, n=%d): code=%d", ErrReservationShort, productID, n, code)
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

// RestockAvailable 退款回补：available += n。available 桶不存在时什么也不做——
// 之后按 DB 水位（已含这次回补）初始化，不能凭空建一个只有 n 的桶。
func RestockAvailable(ctx context.Context, productID uint, n int64) error {
	return restockScript.Run(ctx, RedisClient, []string{StockAvailableKey(productID)}, n).Err()
}

// ScanReservedProductIDs 扫出当前所有存在 reserved 桶的商品 ID，供对账兜底枚举。
// 用 SCAN 增量遍历而非 KEYS，避免大库存下阻塞 Redis 主线程。
func ScanReservedProductIDs(ctx context.Context) ([]uint, error) {
	const pattern = "stock:reserved:*"
	var (
		ids    []uint
		cursor uint64
	)
	for {
		keys, next, err := RedisClient.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			var pid uint
			if _, perr := fmt.Sscanf(k, "stock:reserved:%d", &pid); perr == nil {
				ids = append(ids, pid)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return ids, nil
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
