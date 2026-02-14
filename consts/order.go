package consts

const (
	OrderTypeUnPaid = iota + 1
	OrderTypePendingShipping
	OrderTypeShipping
	OrderTypeReceipt
)

const (
	Unknown = iota
	UnPaid
	Paid
	Cancelled
	Refunded
)

const (
	// StatusUnknown 默认值为 0，代表未初始化或异常状态
	StatusUnknown OrderStatus = iota
	// StatusUnPaid 待支付
	StatusUnPaid // 1
	// StatusPaid 已支付
	StatusPaid // 2
	// StatusCancelled 已取消（超时或手动）
	StatusCancelled // 3
	// StatusRefunded 已退款
	StatusRefunded // 4
)

var OrderTypeMap = map[int]string{
	OrderTypeUnPaid:          "未支付",
	OrderTypePendingShipping: "已支付，待发货",
	OrderTypeShipping:        "已发货，待收货",
	OrderTypeReceipt:         "已收货，交易成功",
}
