package order

// ShipOrderReq 商家发货请求。物流单号 / 承运商必填并落订单表：空单号发货会直接开始 7 天自动收货倒计时。
type ShipOrderReq struct {
	OrderNum   uint64 `form:"order_num" json:"order_num" binding:"required"`
	TrackingNo string `form:"tracking_no" json:"tracking_no" binding:"required,max=64"`
	Carrier    string `form:"carrier" json:"carrier" binding:"required,max=32"`
}

// ConfirmReceiveReq 用户确认收货请求。
type ConfirmReceiveReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
}
