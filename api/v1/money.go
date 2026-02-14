package v1

import (
	"net/http"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/service"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/types"
)

func ShowMoneyHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.MoneyShowReq
		if err := ctx.ShouldBind(&req); err != nil {
			// 参数校验
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}

		l := service.GetMoneySrv()
		resp, err := l.MoneyShow(ctx.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, resp))
	}
}
