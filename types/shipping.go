package types

// ShipOrderReq 商家发货请求。物流单号 / 承运商本期仅经事件透传，不入主表。
type ShipOrderReq struct {
	OrderNum   uint64 `form:"order_num" json:"order_num" binding:"required"`
	TrackingNo string `form:"tracking_no" json:"tracking_no"`
	Carrier    string `form:"carrier" json:"carrier"`
}

// ConfirmReceiveReq 用户确认收货请求。
type ConfirmReceiveReq struct {
	OrderNum uint64 `form:"order_num" json:"order_num" binding:"required"`
}
