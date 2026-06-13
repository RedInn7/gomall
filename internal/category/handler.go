package category

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

func ListCategoryHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ListCategoryReq](ctx)
		if !ok {
			return
		}

		l := GetCategorySrv()
		resp, err := l.CategoryList(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
