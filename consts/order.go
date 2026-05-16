package consts

// 订单状态机。
// 数值兼容：1/2/3 保留原义（待付 / 已付 / 已关闭，存量数据不动）；4-7 为补齐
// 业界 7 态后的新增节点。
const (
	_                = iota // 0 占位，避免业务侧把 0 当成合法状态
	OrderWaitPay            // 1 待付款
	OrderWaitShip           // 2 已付款待发货
	OrderClosed             // 3 已关闭（未支付前取消 / 关单兜底）
	OrderWaitReceive        // 4 已发货待收货
	OrderCompleted          // 5 已完成（用户确认收货 / 7d 自动）
	OrderRefunding          // 6 退款中
	OrderRefunded           // 7 已退款
)

// 旧别名，标 Deprecated 保留兼容；新代码请使用 OrderXxx 命名。
// 这里同时把旧 OrderType* 系列合并进来：OrderTypeShipping 旧值 3 与 Cancelled 撞值
// 但代码里没有任何引用，把它对齐到 OrderWaitReceive(4)；OrderTypeReceipt 同理。
const (
	// Deprecated: use OrderWaitPay.
	UnPaid = OrderWaitPay
	// Deprecated: use OrderWaitShip.
	Paid = OrderWaitShip
	// Deprecated: use OrderClosed.
	Cancelled = OrderClosed
	// Deprecated: use OrderRefunded.
	Refunded = OrderRefunded

	// Deprecated: use OrderWaitPay.
	OrderTypeUnPaid = OrderWaitPay
	// Deprecated: use OrderWaitShip.
	OrderTypePendingShipping = OrderWaitShip
	// Deprecated: use OrderWaitReceive.
	OrderTypeShipping = OrderWaitReceive
	// Deprecated: use OrderCompleted.
	OrderTypeReceipt = OrderCompleted
)

// OrderStateMap 业务侧使用的中文显示名，主要供日志 / 后台展示 / 错误信息使用。
var OrderStateMap = map[uint]string{
	OrderWaitPay:     "待付款",
	OrderWaitShip:    "待发货",
	OrderClosed:      "已关闭",
	OrderWaitReceive: "已发货待收货",
	OrderCompleted:   "已完成",
	OrderRefunding:   "退款中",
	OrderRefunded:    "已退款",
}

// OrderTypeMap 旧前端字典保留兼容。
var OrderTypeMap = map[int]string{
	int(OrderTypeUnPaid):          "未支付",
	int(OrderTypePendingShipping): "已支付，待发货",
	int(OrderTypeShipping):        "已发货，待收货",
	int(OrderTypeReceipt):         "已完成",
}
