package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service/search"
	"github.com/RedInn7/gomall/types"
)

// SemanticSearchProductsHandler 语义检索: embedding + Milvus 向量召回 + ES 关键词召回融合排序
func SemanticSearchProductsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.ProductSemanticSearchReq
		if err := ctx.ShouldBindJSON(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}

		hits, err := search.SemanticSearch(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Errorln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, &types.DataListResp{
			Item:  hits,
			Total: int64(len(hits)),
		}))
	}
}
