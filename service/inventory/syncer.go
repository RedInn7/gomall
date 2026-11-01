package inventory

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// SeedFromDB 启动时把 product.num 复制到 Redis stock:available 桶。
// 已有 available key 的 product 跳过 (避免覆盖运行期变更)。
func SeedFromDB(ctx context.Context, batchSize int) error {
	if batchSize <= 0 {
		batchSize = 500
	}
	d := dao.NewDBClient(ctx)
	var lastID uint
	for {
		var rows []*product.Product
		q := d.Model(&product.Product{}).Order("id ASC").Limit(batchSize)
		if lastID > 0 {
			q = q.Where("id > ?", lastID)
		}
		if err := q.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, p := range rows {
			if err := ctx.Err(); err != nil {
				return err // 进程退出：别对剩下的商品逐个报错
			}
			lastID = p.ID
			// 判断和建桶在一段 Lua 里原子完成：预热和下单时的懒初始化并发，也不会覆盖已有预占
			if err := cache.InitStockIfAbsent(ctx, p.ID, int64(p.Num)); err != nil {
				util.LogrusObj.Errorf("InitStock product=%d failed: %v", p.ID, err)
			}
		}
	}
	return nil
}

// availableReconcileGrace available 桶对账两次采样的间隔：让采样间发生的下单 / 支付 / 取消
// 在第二次采样时已稳定，只有两次采样得到完全一致的漂移才纠偏，过滤掉在途操作造成的瞬时不一致。
var availableReconcileGrace = 5 * time.Second

// ReconcileAvailable 对账 available 桶与 DB product.Num，纠正漂移（Redis 丢键后重建、退款回补 /
// 改库存写 Redis 失败等）。
//
// 不变式（静止态）：available + reserved == product.Num + pending，pending 是已付款扣了 DB、
// reserved 还没核销的件数。打了 StockCommitPending 标记的一定算；最近 StockCommitGrace 内刚付款进 WaitShip、
// 没打标记的可能已核销也可能还在途，所以 pending 只知道一个区间 [lo, hi]，漂移也是区间：
// 整个区间都 > 0 才补（按下界补），整个区间都 < 0 才扣（按上界扣），跨 0 不动。
// 付款高峰时「DB 已扣、reserved 还没核销」的瞬间不会被当成 available 多了而扣掉：
//   - 下单 available→reserved、取消 reserved→available、支付 commit 时 reserved 与 DB.Num 同步减，和都不变；
//   - available 可以为负（商家把库存改到比已预占还少，见 cache.AdjustAvailable），纠偏不夹到 0，
//     否则欠账丢失，之后释放回来的预占会凭空多出可卖库存。
//
// 防误纠：先整批采样，静默 availableReconcileGrace 后在 WATCH(available,reserved) 里重新读 DB 与 Redis
// 再算一次，两次漂移完全一致才写入，计算到写入之间桶被改写则事务作废。
// 多实例 / 任务重叠时，先写入的那次让后来者第三次算出的漂移变成 0，不会重复纠偏。
func ReconcileAvailable(ctx context.Context, batchSize int) error {
	if cache.RedisClient == nil {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	d := dao.NewDBClient(ctx)
	lo, hi, err := commitPendingBounds(ctx)
	if err != nil {
		return err
	}
	drifts := make(map[uint]int64)
	var lastID uint
	for {
		var rows []*product.Product
		q := d.Model(&product.Product{}).Order("id ASC").Limit(batchSize)
		if lastID > 0 {
			q = q.Where("id > ?", lastID)
		}
		if err := q.Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, p := range rows {
			lastID = p.ID
			if drift, ok := availableDrift(ctx, p.ID, int64(p.Num), lo[p.ID], hi[p.ID]); ok && drift != 0 {
				drifts[p.ID] = drift
			}
		}
	}
	if len(drifts) == 0 {
		return nil
	}

	time.Sleep(availableReconcileGrace)
	for pid, drift := range drifts {
		applied, err := applyAvailableDrift(ctx, pid, drift)
		if err != nil {
			util.LogrusObj.Errorf("ReconcileAvailable 纠偏失败 product=%d drift=%d err=%v", pid, drift, err)
			continue
		}
		if applied {
			util.LogrusObj.Warnf("ReconcileAvailable 纠正 available 漂移 product=%d drift=%d", pid, drift)
		}
	}
	return nil
}

// commitPendingBounds 已付款、reserved 还没核销的件数区间：lo 只算打了标记的，hi 再加上最近刚付款的。
func commitPendingBounds(ctx context.Context) (lo, hi map[uint]int64, err error) {
	od := order.NewOrderDao(ctx)
	if lo, err = od.SumStockCommitPendingNumByProduct(); err != nil {
		return nil, nil, err
	}
	hi, err = od.SumCommitPendingNumByProduct(time.Now().Add(-order.StockCommitGrace))
	return lo, hi, err
}

// pickDrift 漂移区间 [base+lo, base+hi] 整体同号才纠，取离 0 近的一端。
func pickDrift(base, lo, hi int64) int64 {
	switch {
	case base+lo > 0:
		return base + lo
	case base+hi < 0:
		return base + hi
	}
	return 0
}

// availableDrift 计算 drift = (Num + pending - reserved) - available（pending 取区间，见 pickDrift）。
// drift>0 表示 available 少了需补，<0 表示多了需扣。available 桶不存在（未 seed）时跳过。
func availableDrift(ctx context.Context, productID uint, num, lo, hi int64) (int64, bool) {
	available, e1 := cache.RedisClient.Get(ctx, cache.StockAvailableKey(productID)).Int64()
	if e1 == redis.Nil {
		return 0, false // 未初始化的商品交给 SeedFromDB / 下单懒建桶
	}
	if e1 != nil {
		util.LogrusObj.Errorf("ReconcileAvailable 读 available 失败 product=%d err=%v", productID, e1)
		return 0, false
	}
	reserved, e2 := cache.RedisClient.Get(ctx, cache.StockReservedKey(productID)).Int64()
	if e2 != nil && e2 != redis.Nil {
		util.LogrusObj.Errorf("ReconcileAvailable 读 reserved 失败 product=%d err=%v", productID, e2)
		return 0, false
	}
	return pickDrift(num-reserved-available, lo, hi), true
}

// applyAvailableDrift 在 WATCH(available,reserved) 下重新读 DB 与 Redis 算一次漂移，仍等于 drift 才写入。
func applyAvailableDrift(ctx context.Context, productID uint, drift int64) (bool, error) {
	p, err := product.NewProductDao(ctx).GetProductById(productID)
	if err != nil {
		return false, err
	}
	lo, hi, err := commitPendingBounds(ctx)
	if err != nil {
		return false, err
	}
	availKey := cache.StockAvailableKey(productID)
	reservedKey := cache.StockReservedKey(productID)
	applied := false
	err = cache.RedisClient.Watch(ctx, func(tx *redis.Tx) error {
		cur, err := tx.Get(ctx, availKey).Int64()
		if err != nil {
			return err
		}
		reserved, err := tx.Get(ctx, reservedKey).Int64()
		if err != nil && err != redis.Nil {
			return err
		}
		if pickDrift(int64(p.Num)-reserved-cur, lo[productID], hi[productID]) != drift {
			return nil // 采样后状态又变了，本轮放弃
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, availKey, cur+drift, 0)
			return nil
		})
		applied = err == nil
		return err
	}, availKey, reservedKey)
	return applied, err
}
