package payment

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

// CryptoPaydownNonceHandler 签名前的 nonce 颁发。
// 一次请求一次 nonce，5 分钟有效，到期或被消费后不可复用
func CryptoPaydownNonceHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.BindQuery[CryptoNonceReq](c)
		if !ok {
			return
		}
		resp, err := GetCryptoPaymentSrv().IssueNonce(c.Request.Context(), req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}

// CryptoPaydownHandler 钱包签名 → 后端验签 → outbox。
// 真正的链上确认由独立 listener 兜底，本接口仅返回 pending
func CryptoPaydownHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := response.BindJSON[CryptoPaydownReq](c)
		if !ok {
			return
		}
		resp, err := GetCryptoPaymentSrv().VerifyAndPark(c.Request.Context(), req)
		if err != nil {
			response.Fail(c, err)
			return
		}
		response.OK(c, resp)
	}
}
