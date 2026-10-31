package product

import (
	"context"
	"errors"
	"fmt"

	"github.com/RedInn7/gomall/repository/cache"
)

// ReserveStock 下单预占库存（available -> reserved）。同步 / 异步下单、预售、拼团统一走这里，
// 不直接调 cache.ReserveStock。
// 库存桶不存在（启动预热还没扫到、建商品时写 Redis 失败、绕过商品接口直接写库的商品）时，
// 按 DB 的 num 建桶后再试一次，只重试一次。
func ReserveStock(ctx context.Context, productID uint, n int64) error {
	err := cache.ReserveStock(ctx, productID, n)
	if !errors.Is(err, cache.ErrStockNotInit) {
		return err
	}
	p, err := NewProductDao(ctx).GetProductById(productID)
	if err != nil {
		return fmt.Errorf("商品不存在或查询失败 product=%d: %w", productID, err)
	}
	if err = cache.InitStockIfAbsent(ctx, productID, int64(p.Num)); err != nil {
		return err
	}
	return cache.ReserveStock(ctx, productID, n)
}
