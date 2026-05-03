package types

type PaymentDownReq struct {
	OrderId uint   `form:"order_id" json:"order_id" binding:"required"`
	Key     string `form:"key" json:"key" binding:"required"`
}
