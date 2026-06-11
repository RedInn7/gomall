package carousel

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

func ListCarouselsHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req ListCarouselReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}

		l := GetCarouselSrv()
		resp, err := l.ListCarousel(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}
