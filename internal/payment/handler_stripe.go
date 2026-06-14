package payment

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

// StripeCheckoutHandler 发起 Stripe 托管支付，返回支付页 URL。需登录。
func StripeCheckoutHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[StripeCheckoutReq](ctx)
		if !ok {
			return
		}
		resp, err := GetStripePaymentSrv().CreateCheckout(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, resp)
	}
}

// StripeWebhookHandler 接收 Stripe webhook。公开端点（无登录），靠签名校验保证来源。
// 用原生 HTTP 状态码而非业务信封：签名失败 400；处理失败 5xx 让 Stripe 重投；成功 200。
func StripeWebhookHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		payload, err := ctx.GetRawData()
		if err != nil {
			ctx.String(http.StatusBadRequest, "read body failed")
			return
		}
		sig := ctx.GetHeader("Stripe-Signature")

		err = GetStripePaymentSrv().HandleWebhook(ctx.Request.Context(), payload, sig)
		switch {
		case err == nil:
			ctx.String(http.StatusOK, "ok")
		case errors.Is(err, ErrStripeSignature):
			// 签名校验失败：请求可疑，拒绝且无需重投。
			log.LogrusObj.Errorf("stripe webhook signature error: %v", err)
			ctx.String(http.StatusBadRequest, "invalid signature")
		default:
			// 结算 / 配置 / 解析失败：回 5xx 让 Stripe 重投（结算幂等，重投安全）。
			log.LogrusObj.Errorf("stripe webhook error: %v", err)
			ctx.String(http.StatusInternalServerError, "webhook processing failed")
		}
	}
}
