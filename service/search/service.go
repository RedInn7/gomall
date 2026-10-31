package search

import (
	"context"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/repository/es"
)

// SearchProducts 调用 ES 多字段模糊匹配。keyword 取请求里的 info / title / name 任意非空字段
func SearchProducts(ctx context.Context, req *product.ProductSearchReq) ([]*es.ProductDoc, int64, error) {
	kw := searchKeyword(req)
	req.BasePage.Normalize()
	from := (req.PageNum - 1) * req.PageSize
	return es.SearchProducts(ctx, kw, from, req.PageSize)
}

// searchKeyword ES 与 DB 回退共用的关键词，优先级 info > title > name
func searchKeyword(req *product.ProductSearchReq) string {
	return firstNonEmpty(req.Info, req.Title, req.Name)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
