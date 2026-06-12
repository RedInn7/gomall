package consts

// 订单状态机。
// 数值兼容：1/2/3 保留原义（待付 / 已付 / 已关闭，存量数据不动）；4-7 为补齐
// 业界 7 态后的新增节点；8 为拼团成员订单等待成团的过渡态。
const (
	_                = iota // 0 占位，避免业务侧把 0 当成合法状态
	OrderWaitPay            // 1 待付款
	OrderWaitShip           // 2 已付款待发货
	OrderClosed             // 3 已关闭（未支付前取消 / 关单兜底）
	OrderWaitReceive        // 4 已发货待收货
	OrderCompleted          // 5 已完成（用户确认收货 / 7d 自动）
	OrderRefunding          // 6 退款中
	OrderRefunded           // 7 已退款
	OrderWaitGroup          // 8 拼团中（成员订单凑齐 N 人前的过渡态）
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
	OrderWaitGroup:   "拼团中",
}
