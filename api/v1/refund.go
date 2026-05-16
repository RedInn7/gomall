package v1

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service"
	"github.com/RedInn7/gomall/types"
)

// RequestRefundHandler 用户发起退款。允许 from: WaitShip / WaitReceive / Completed。
func RequestRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.RequestRefundReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		if err := service.GetRefundSrv().RequestRefund(ctx.Request.Context(), req.OrderNum, req.Reason); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, gin.H{"order_num": req.OrderNum}))
	}
}

// ApproveRefundHandler 运营同意退款：Refunding -> Refunded。
// 真正的退款扣款由下游 wallet/支付服务消费 outbox 事件后落地。
func ApproveRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.ApproveRefundReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		if err := service.GetRefundSrv().ApproveRefund(ctx.Request.Context(), req.OrderNum); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, gin.H{"order_num": req.OrderNum}))
	}
}

// RejectRefundHandler 运营驳回退款：Refunding -> Completed。
func RejectRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		var req types.RejectRefundReq
		if err := ctx.ShouldBind(&req); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		if err := service.GetRefundSrv().RejectRefund(ctx.Request.Context(), req.OrderNum, req.Reason); err != nil {
			log.LogrusObj.Infoln(err)
			ctx.JSON(http.StatusOK, ErrorResponse(ctx, err))
			return
		}
		ctx.JSON(http.StatusOK, ctl.RespSuccess(ctx, gin.H{"order_num": req.OrderNum}))
	}
}
