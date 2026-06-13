package refund

import (
	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/internal/shared/response"
)

// RequestRefundHandler 用户发起退款。允许 from: WaitShip / WaitReceive / Completed。
func RequestRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[RequestRefundReq](ctx)
		if !ok {
			return
		}
		if err := GetRefundSrv().RequestRefund(ctx.Request.Context(), req.OrderNum, req.Reason); err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"order_num": req.OrderNum})
	}
}

// ApproveRefundHandler 运营同意退款：Refunding -> Refunded。
// 真正的退款扣款由下游 wallet/支付服务消费 outbox 事件后落地。
func ApproveRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ApproveRefundReq](ctx)
		if !ok {
			return
		}
		if err := GetRefundSrv().ApproveRefund(ctx.Request.Context(), req.OrderNum); err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"order_num": req.OrderNum})
	}
}

// RejectRefundHandler 运营驳回退款：Refunding -> Completed。
func RejectRefundHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[RejectRefundReq](ctx)
		if !ok {
			return
		}
		if err := GetRefundSrv().RejectRefund(ctx.Request.Context(), req.OrderNum, req.Reason); err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"order_num": req.OrderNum})
	}
}
