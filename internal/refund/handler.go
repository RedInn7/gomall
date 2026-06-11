package refund

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

// RequestRefundHandler 用户发起退款。允许 from: WaitShip / WaitReceive / Completed。
func RequestRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req RequestRefundReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		if err := GetRefundSrv().RequestRefund(ctx.Request.Context(), req.OrderNum, req.Reason); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, gin.H{"order_num": req.OrderNum}))
	}
}

// ApproveRefundHandler 运营同意退款：Refunding -> Refunded。
// 真正的退款扣款由下游 wallet/支付服务消费 outbox 事件后落地。
func ApproveRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req ApproveRefundReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		if err := GetRefundSrv().ApproveRefund(ctx.Request.Context(), req.OrderNum); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, gin.H{"order_num": req.OrderNum}))
	}
}

// RejectRefundHandler 运营驳回退款：Refunding -> Completed。
func RejectRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req RejectRefundReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		if err := GetRefundSrv().RejectRefund(ctx.Request.Context(), req.OrderNum, req.Reason); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, response.ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, gin.H{"order_num": req.OrderNum}))
	}
}
