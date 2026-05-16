package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service"
	"github.com/RedInn7/gomall/types"
)

// CryptoPaydownNonceHandler 签名前的 nonce 颁发。
// 一次请求一次 nonce，5 分钟有效，到期或被消费后不可复用
func CryptoPaydownNonceHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req types.CryptoNonceReq
		if err := c.ShouldBindQuery(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		resp, err := service.GetCryptoPaymentSrv().IssueNonce(c.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}

// CryptoPaydownHandler 钱包签名 → 后端验签 → outbox。
// 真正的链上确认由独立 listener 兜底，本接口仅返回 pending
func CryptoPaydownHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req types.CryptoPaydownReq
		if err := c.ShouldBindJSON(&req); err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		resp, err := service.GetCryptoPaymentSrv().VerifyAndPark(c.Request.Context(), &req)
		if err != nil {
			log.LogrusObj.Infoln(err)
			c.JSON(http.StatusOK, ErrorResponse(c, err))
			return
		}
		c.JSON(http.StatusOK, ctl.RespSuccess(c, resp))
	}
}
