package money

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

func ShowMoneyHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[MoneyShowReq](ctx)
		if !ok {
			return
		}

		l := GetMoneySrv()
		resp, err := l.MoneyShow(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
