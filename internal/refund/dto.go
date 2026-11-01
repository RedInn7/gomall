package refund

// RequestRefundReq 用户发起退款。reason 必填，落订单表给商家审批时看。
type RequestRefundReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
	Reason   string `form:"reason" json:"reason" binding:"required,max=255"`
}

// ApproveRefundReq 运营 / 商家同意退款。
type ApproveRefundReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
}

// RejectRefundReq 运营 / 商家驳回退款，订单回到申请前的状态（WaitShip / WaitReceive / Completed）。
type RejectRefundReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
	Reason   string `form:"reason" json:"reason"`
}

// ReturnReceivedReq 商家确认退货入库。
type ReturnReceivedReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
}
