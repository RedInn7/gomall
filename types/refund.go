package types

// RequestRefundReq 用户发起退款。reason 透传给客服 / 风控。
type RequestRefundReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
	Reason   string `form:"reason" json:"reason"`
}

// ApproveRefundReq 运营 / 商家同意退款。
type ApproveRefundReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
}

// RejectRefundReq 运营 / 商家驳回退款，订单回到 Completed。
type RejectRefundReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
	Reason   string `form:"reason" json:"reason"`
}
