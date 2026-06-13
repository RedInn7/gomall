package carousel

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

func ListCarouselsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ListCarouselReq](ctx)
		if !ok {
			return
		}

		l := GetCarouselSrv()
		resp, err := l.ListCarousel(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
