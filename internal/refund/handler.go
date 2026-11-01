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

// RejectRefundHandler 运营驳回退款：Refunding -> 申请前的状态（WaitShip / WaitReceive / Completed）。
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

// ReturnReceivedHandler 商家确认退货入库，回补库存（仅发货后才退款的订单）。
func ReturnReceivedHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.Bind[ReturnReceivedReq](ctx)
		if !ok {
			return
		}
		if err := GetRefundSrv().ConfirmReturnReceived(ctx.Request.Context(), req.OrderNum); err != nil {
			response.Fail(ctx, err)
			return
		}
		response.OK(ctx, gin.H{"order_num": req.OrderNum})
	}
}

// ListRefundingHandler 商家查看待审批退款单，带买家填的退款原因。
func ListRefundingHandler() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		req, ok := response.BindQuery[RefundListReq](ctx)
		if !ok {
			return
		}
		orders, err := GetRefundSrv().ListRefunding(ctx.Request.Context(), req.LastID, req.PageSize)
		if err != nil {
			response.Fail(ctx, err)
			return
		}
		list := make([]RefundListItem, 0, len(orders))
		for _, o := range orders {
			list = append(list, RefundListItem{
				ID: o.ID, OrderNum: o.OrderNum, UserID: o.UserID, ProductID: o.ProductID, Num: o.Num,
				Amount: refundAmount(o), FromType: o.RefundFromType, Reason: o.RefundReason,
				RefundCount: o.RefundCount, TrackingNo: o.TrackingNo, UpdatedAt: o.UpdatedAt,
			})
		}
		response.OK(ctx, gin.H{"list": list})
	}
}
