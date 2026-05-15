package inventory

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
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
		var rows []*model.Product
		q := d.Model(&model.Product{}).Order("id ASC").Limit(batchSize)
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
