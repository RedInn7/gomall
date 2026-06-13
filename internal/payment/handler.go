package payment

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

func OrderPaymentHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[PaymentDownReq](ctx)
		if !ok {
			return
		}

		l := GetPaymentSrv()
		resp, err := l.PayDown(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}
