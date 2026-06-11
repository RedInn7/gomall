package search

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/types"
)

// SearchProductsHandler 搜索商品（ES 关键词检索，降级 DB）
func SearchProductsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req product.ProductSearchReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		if req.PageSize == 0 {
			req.PageSize = consts.BasePageSize
		}

		resp, err := ProductSearch(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}

// SemanticSearchProductsHandler 语义检索: embedding + Milvus 向量召回 + ES 关键词召回融合排序
func SemanticSearchProductsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req product.ProductSemanticSearchReq
		if err := ctx.ShouldBindJSON(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		hits, err := SemanticSearch(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Errorln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, &types.DataListResp{
			Item:  hits,
			Total: int64(len(hits)),
		}))
	}
}
