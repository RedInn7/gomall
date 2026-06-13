package search

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/types"
)

// SearchProductsHandler 搜索商品（ES 关键词检索，降级 DB）
func SearchProductsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[product.ProductSearchReq](ctx)
		if !ok {
			return
		}
		if req.PageSize == 0 {
			req.PageSize = consts.BasePageSize
		}

		resp, err := ProductSearch(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// SemanticSearchProductsHandler 语义检索: embedding + Milvus 向量召回 + ES 关键词召回融合排序
func SemanticSearchProductsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.BindJSON[product.ProductSemanticSearchReq](ctx)
		if !ok {
			return
		}

		hits, err := SemanticSearch(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, &types.DataListResp{
			Item:  hits,
			Total: int64(len(hits)),
		})
	}
}
