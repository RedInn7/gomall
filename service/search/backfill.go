package search

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/repository/es"
)

// BackfillFromDB 把 product 表全量导入 ES，admin 接口手动触发
func BackfillFromDB(ctx context.Context, batchSize int) (indexed int, err error) {
	if batchSize <= 0 {
		batchSize = 200
	}
	if err := es.EnsureProductIndex(ctx); err != nil {
		return 0, err
	}
	db := dao.NewDBClient(ctx)
	var lastID uint
	for {
		var rows []*model.Product
		q := db.Model(&model.Product{}).Order("id ASC").Limit(batchSize)
		if lastID > 0 {
			q = q.Where("id > ?", lastID)
		}
		if e := q.Find(&rows).Error; e != nil {
			return indexed, e
		}
		if len(rows) == 0 {
			return indexed, nil
		}
		for _, p := range rows {
			if e := es.UpsertProduct(ctx, p); e != nil {
				util.LogrusObj.Errorf("backfill upsert product=%d failed: %v", p.ID, e)
				continue
			}
			indexed++
			lastID = p.ID
		}
	}
}
