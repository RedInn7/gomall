package inventory

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

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
			lastID = p.ID
			exists, err := cache.RedisClient.Exists(ctx, cache.StockAvailableKey(p.ID)).Result()
			if err != nil {
				util.LogrusObj.Errorln("inventory exists check:", err)
				continue
			}
			if exists == 1 {
				continue
			}
			if err := cache.InitStock(ctx, p.ID, int64(p.Num)); err != nil {
				util.LogrusObj.Errorf("InitStock product=%d failed: %v", p.ID, err)
			}
		}
	}
	return nil
}

// availableReconcileGrace available 桶对账两次采样的静默期：让采样间发生的下单 / 支付 / 取消
// 在第二次采样时已稳定，只有两次采样得到完全一致的漂移才纠偏，过滤掉在途操作造成的瞬时不一致。
const availableReconcileGrace = 5 * time.Second

// ReconcileAvailable 对账 available 桶与 DB product.Num，纠正漂移。
//
// 不变式（静止态）：available + reserved == product.Num。各路径都维持它：
//   - 初始化 available=Num,reserved=0；下单 available→reserved（和不变）；
//   - 取消 reserved→available（和不变）；支付 commit 时 reserved 与 DB.Num 同步减（和不变）。
//
// 既有对账只盯 reserved 桶，available 与 DB.Num 的漂移（如 Redis 丢键后被 InitStock 覆盖、
// 历史脏数据）无人纠正。本任务按 expected = Num - reserved 核对 available，仅当两次采样得到
// 完全一致的漂移量时，才用 WATCH/MULTI 把差额原子补到 available 上。
//
// 防误纠的三重保险：
//  1. 两次采样取一致（在途下单 / 支付会改变第二次的漂移量，从而被跳过）；
//  2. 纠偏走 WATCH(available,reserved)+MULTI，纠偏期间任一桶被改写则事务作废、本轮放弃；
//  3. 仅纠偏，绝不把 available 调成负数。
//
// 注意：本任务读 DB.Num 与读 Redis 之间也存在窗口，故依赖第 1、2 两点过滤在途，宁可漏纠
// 一轮（下一轮再纠），也不在并发下错纠出超卖。
func ReconcileAvailable(ctx context.Context, batchSize int) error {
	if cache.RedisClient == nil {
		return nil
	}
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
			lastID = p.ID
			drift1, ok := availableDrift(ctx, p.ID, int64(p.Num))
			if !ok || drift1 == 0 {
				continue
			}
			time.Sleep(availableReconcileGrace)
			drift2, ok := availableDrift(ctx, p.ID, int64(p.Num))
			// 两次漂移必须完全一致：不一致说明采样间有在途操作，本轮跳过。
			if !ok || drift2 != drift1 {
				continue
			}
			if err := applyAvailableDrift(ctx, p.ID, drift1); err != nil {
				util.LogrusObj.Errorf("ReconcileAvailable 纠偏失败 product=%d drift=%d err=%v", p.ID, drift1, err)
				continue
			}
			util.LogrusObj.Warnf("ReconcileAvailable 纠正 available 漂移 product=%d drift=%d（available 调整为 Num-reserved）", p.ID, drift1)
		}
	}
	return nil
}

// availableDrift 计算单个商品 available 期望值与现值的差：drift = (Num - reserved) - available。
// drift>0 表示 available 少了需补，<0 表示多了需扣。available 桶不存在（未 seed）时跳过。
func availableDrift(ctx context.Context, productID uint, dbNum int64) (int64, bool) {
	available, e1 := cache.RedisClient.Get(ctx, cache.StockAvailableKey(productID)).Int64()
	if e1 == redis.Nil {
		return 0, false // 未初始化的商品交给 SeedFromDB，不在此纠偏
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
	return (dbNum - reserved) - available, true
}

// applyAvailableDrift 在 WATCH(available,reserved) 下把 drift 原子补到 available。
// 纠偏期间任一桶被并发改写则 MULTI 作废（返回 TxFailedErr），本轮放弃、下轮再来，杜绝并发错纠。
// 结果会 clamp 到 >=0，绝不把 available 调成负数。
func applyAvailableDrift(ctx context.Context, productID uint, drift int64) error {
	availKey := cache.StockAvailableKey(productID)
	reservedKey := cache.StockReservedKey(productID)
	return cache.RedisClient.Watch(ctx, func(tx *redis.Tx) error {
		cur, err := tx.Get(ctx, availKey).Int64()
		if err != nil {
			return err
		}
		target := cur + drift
		if target < 0 {
			target = 0
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, availKey, target, 0)
			return nil
		})
		return err
	}, availKey, reservedKey)
}
