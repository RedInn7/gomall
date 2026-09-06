package search

import (
	"context"

	"github.com/RedInn7/gomall/internal/product"
	util "github.com/RedInn7/gomall/pkg/utils/log"
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
	productDao := product.NewProductDao(ctx)
	var lastID uint
	for {
		rows, e := productDao.ListAfterID(lastID, batchSize)
		if e != nil {
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
			if store, enabled := getProductVectorStore(); enabled {
				vec, e := EmbedText(ctx, productEmbeddingText(p))
				if e != nil {
					return indexed, e
				}
				if e := store.Upsert(ctx, p.ID, vec, p.CategoryID); e != nil {
					return indexed, e
				}
			}
			indexed++
			lastID = p.ID
		}
	}
}
