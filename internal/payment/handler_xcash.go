package payment

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

func XcashCheckoutHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.BindJSON[XcashCheckoutReq](ctx)
		if !ok {
			return
		}
		result, err := GetXcashPaymentSrv().CreateCheckout(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, result)
	}
}

func XcashCheckoutStatusHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.BindQuery[XcashCheckoutReq](ctx)
		if !ok {
			return
		}
		result, err := GetXcashPaymentSrv().GetCheckout(ctx.Request.Context(), req)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, result)
	}
}

// XcashWebhookHandler 必须返回纯文本 "ok" 才会被 Xcash 视为成功。
// 伪造或格式错误的通知返回 400；数据库/RPC 瞬时错误返回 500，触发指数退避重试。
func XcashWebhookHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxXcashBody)
		payload, err := io.ReadAll(ctx.Request.Body)
		if err != nil {
			ctx.String(http.StatusBadRequest, "invalid body")
			return
		}
		headers := xcashWebhookHeaders{
			AppID: ctx.GetHeader("XC-Appid"), Nonce: ctx.GetHeader("XC-Nonce"),
			Timestamp: ctx.GetHeader("XC-Timestamp"), Signature: ctx.GetHeader("XC-Signature"),
		}
		err = GetXcashPaymentSrv().HandleWebhook(ctx.Request.Context(), headers, payload)
		switch {
		case err == nil:
			ctx.String(http.StatusOK, "ok")
		case errors.Is(err, ErrXcashSignature), errors.Is(err, ErrXcashTimestamp), errors.Is(err, ErrXcashInvalidWebhook):
			log.LogrusObj.Warnf("reject Xcash webhook: %v", err)
			ctx.String(http.StatusBadRequest, "invalid webhook")
		default:
			log.LogrusObj.Errorf("Xcash webhook processing failed: %v", err)
			ctx.String(http.StatusInternalServerError, "webhook processing failed")
		}
	}
}
