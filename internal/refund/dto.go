package refund

import "time"

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

// RefundListReq 商家待审批退款列表，last_id 翻页。
type RefundListReq struct {
	LastID   uint `form:"last_id" json:"last_id"`
	PageSize int  `form:"page_size" json:"page_size"`
}

// RefundListItem 待审批退款单。
type RefundListItem struct {
	ID          uint      `json:"id"`
	OrderNum    uint64    `json:"order_num"`
	UserID      uint      `json:"user_id"`
	ProductID   uint      `json:"product_id"`
	Num         int       `json:"num"`
	Amount      int64     `json:"amount"` // 分，与实际退款同口径
	FromType    uint      `json:"from_type"`
	Reason      string    `json:"reason"`
	RefundCount int       `json:"refund_count"`
	TrackingNo  string    `json:"tracking_no"`
	UpdatedAt   time.Time `json:"updated_at"`
}
