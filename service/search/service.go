package search

import (
	"context"

	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/types"
)

// SearchProducts 调用 ES 多字段模糊匹配。keyword 取请求里的 info / title / name 任意非空字段
func SearchProducts(ctx context.Context, req *types.ProductSearchReq) ([]*es.ProductDoc, int64, error) {
	kw := firstNonEmpty(req.Info, req.Title, req.Name)
	req.BasePage.Normalize()
	from := (req.PageNum - 1) * req.PageSize
	return es.SearchProducts(ctx, kw, from, req.PageSize)
}

// Backfill 把 DB 中现存商品全量灌进 ES (按 id 批量扫)
func Backfill(ctx context.Context, batchSize int, fetcher func(lastID uint, limit int) ([]uint, error), loader func(id uint) error) error {
	if batchSize <= 0 {
		batchSize = 200
	}
	var last uint
	for {
		ids, err := fetcher(last, batchSize)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if err := loader(id); err != nil {
				return err
			}
			last = id
		}
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
