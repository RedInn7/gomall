package events

// 业务事件 payload。routing key 与类型一一对应:
//   order.created          -> OrderCreated
//   order.paid             -> OrderPaid
//   order.cancelled        -> OrderCancelled
//   order.shipped          -> OrderShipped
//   order.completed        -> OrderCompletedEvent
//   order.refunding        -> OrderRefunding
//   order.refunded         -> OrderRefundedEvent
//   order.refund_rejected  -> OrderRefundRejected
//   product.changed        -> ProductChanged
//   web3.payment.pending   -> Web3PaymentPending

type OrderCreated struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Num       int    `json:"num"`
}

type OrderPaid struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Num       int    `json:"num"`
}

type OrderCancelled struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Num       int    `json:"num"`
	Reason    string `json:"reason"`
}

// OrderShipped 商家发货事件。tracking_no / carrier 仅在事件内透传，本期不入主表。
type OrderShipped struct {
	OrderID    uint   `json:"order_id"`
	OrderNum   uint64 `json:"order_num"`
	UserID     uint   `json:"user_id"`
	TrackingNo string `json:"tracking_no"`
	Carrier    string `json:"carrier"`
}

// OrderCompletedEvent 订单完成事件。用户主动确认或 cron 7d 兜底都会产生这个事件。
// 注意：类型加 Event 后缀是为了避免和 consts.OrderCompleted 常量重名。
type OrderCompletedEvent struct {
	OrderID  uint   `json:"order_id"`
	OrderNum uint64 `json:"order_num"`
	UserID   uint   `json:"user_id"`
	Auto     bool   `json:"auto"` // true=cron 自动确认; false=用户主动确认
}

// OrderRefunding 用户发起退款，下游可用来冻结评价 / 通知商家。
type OrderRefunding struct {
	OrderID  uint   `json:"order_id"`
	OrderNum uint64 `json:"order_num"`
	UserID   uint   `json:"user_id"`
	FromType uint   `json:"from_type"` // 进入退款时的原状态：WaitShip / WaitReceive / Completed
	Reason   string `json:"reason"`
}

// OrderRefundedEvent 退款已同意。Amount 单位：分；TxID 由后续 wallet/支付服务回填。
// 本期只走状态机推进，不真触发第三方扣款。
type OrderRefundedEvent struct {
	OrderID  uint   `json:"order_id"`
	OrderNum uint64 `json:"order_num"`
	UserID   uint   `json:"user_id"`
	Amount   int64  `json:"amount"`
	TxID     string `json:"tx_id"`
}

// OrderRefundRejected 退款被运营驳回，订单退回 Completed。
type OrderRefundRejected struct {
	OrderID  uint   `json:"order_id"`
	OrderNum uint64 `json:"order_num"`
	UserID   uint   `json:"user_id"`
	Reason   string `json:"reason"`
}

type ProductChanged struct {
	ProductID uint   `json:"product_id"`
	Op        string `json:"op"` // create / update / delete
}

// Web3PaymentPending 钱包签名校验通过后写入。订单仍处于 UnPaid 状态，
// 等待链上 listener 收到 PaymentConfirmed 后再把订单推到已支付。
type Web3PaymentPending struct {
	OrderID    uint   `json:"order_id"`
	OrderNum   uint64 `json:"order_num"`
	UserID     uint   `json:"user_id"`
	ProductID  uint   `json:"product_id"`
	Num        int    `json:"num"`
	Amount     int64  `json:"amount"` // 单位：分
	WalletAddr string `json:"wallet_addr"`
	ChainID    uint64 `json:"chain_id"`
	Nonce      string `json:"nonce"`
}

// 红包相关事件：
//   red_packet.created  -> RedPacketCreated  (发包，便于风控/数据同步)
//   red_packet.claimed  -> RedPacketClaimed  (领包，下游钱包消费此事件入账)
//   red_packet.expired  -> RedPacketExpired  (过期回收，退给发包人)

type RedPacketCreated struct {
	RedPacketID uint   `json:"red_packet_id"`
	UserID      uint   `json:"user_id"`
	Total       int64  `json:"total"`
	Count       int    `json:"count"`
	Greeting    string `json:"greeting,omitempty"`
	ExpireAt    int64  `json:"expire_at"` // unix
}

type RedPacketClaimed struct {
	RedPacketID uint  `json:"red_packet_id"`
	UserID      uint  `json:"user_id"`
	Amount      int64 `json:"amount"`
}

type RedPacketExpired struct {
	RedPacketID uint  `json:"red_packet_id"`
	UserID      uint  `json:"user_id"` // 退款回到发包人
	RefundTotal int64 `json:"refund_total"`
}

// 满减引擎事件：
//   promo.applied  -> PromoApplied   下单事务内成功扣减预算后写入
//   promo.released -> PromoReleased  关单 / 退款时退还预算后写入

type PromoApplied struct {
	OrderID       uint  `json:"order_id"`
	RuleID        uint  `json:"rule_id"`
	DiscountCents int64 `json:"discount_cents"`
}

type PromoReleased struct {
	OrderID       uint   `json:"order_id"`
	RuleID        uint   `json:"rule_id"`
	DiscountCents int64  `json:"discount_cents"`
	Reason        string `json:"reason"` // cancel / refund / manual
}

// 拼团事件：
//   groupbuy.created  -> GroupbuyCreated  团长发起，含商品 / 目标人数 / 价格 / 截止
//   groupbuy.joined   -> GroupbuyJoined   成员加入，库存预扣已落
//   groupbuy.success  -> GroupbuySuccess  凑齐 N 人，成员订单 WaitGroup → WaitShip
//   groupbuy.expired  -> GroupbuyExpired  24h 散团，成员订单 → Closed，库存归还
//
// 下游订阅者：
//   - 通知服务：短信 / 推送告知成团 / 散团
//   - 钱包：散团触发原路退款
//   - 数据：宽表落地，做团购 GMV / 散团率分析

type GroupbuyCreated struct {
	GroupID     uint  `json:"group_id"`
	ProductID   uint  `json:"product_id"`
	LeaderID    uint  `json:"leader_id"`
	TargetCount int   `json:"target_count"`
	PriceCents  int64 `json:"price_cents"`
	ExpireAt    int64 `json:"expire_at"` // unix
}

type GroupbuyJoined struct {
	GroupID      uint  `json:"group_id"`
	UserID       uint  `json:"user_id"`
	OrderID      int64 `json:"order_id"`
	CurrentCount int   `json:"current_count"`
	TargetCount  int   `json:"target_count"`
}

type GroupbuySuccess struct {
	GroupID   uint    `json:"group_id"`
	ProductID uint    `json:"product_id"`
	MemberIDs []uint  `json:"member_ids"`
	OrderIDs  []int64 `json:"order_ids"`
}

type GroupbuyExpired struct {
	GroupID   uint    `json:"group_id"`
	ProductID uint    `json:"product_id"`
	Reason    string  `json:"reason"` // timeout / manual
	OrderIDs  []int64 `json:"order_ids"`
}

// 预售两段式支付事件：
//   preorder.deposit.paid   -> PreorderDepositPaid   定金到账 + reserved 已锁
//   preorder.final.paid     -> PreorderFinalPaid     尾款到账 + reserved -> sold
//   preorder.forfeited      -> PreorderForfeited     尾款逾期，定金没收 + 库存归还
//   preorder.cancelled      -> PreorderCancelled     定金期内取消，全额退款 + 库存归还
//
// 下游订阅者：
//   - 通知服务：尾款期开始前 24h push 提醒；逾期没收发"定金不退"提示
//   - 钱包 / 财务：deposit / final 入账；forfeited 计入平台收益 + 商家滞销补贴
//   - 商家后台：备货决策（凭 deposit_paid 数量提前下单）
//   - 数据：预售转化漏斗（deposit -> final / forfeited / cancelled）

type PreorderDepositPaid struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Deposit   int64  `json:"deposit"` // 已收金额，单位：分
}

type PreorderFinalPaid struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Final     int64  `json:"final"` // 本次扣的尾款，单位：分
	Total     int64  `json:"total"` // 定金 + 尾款合计
}

type PreorderForfeited struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Deposit   int64  `json:"deposit"` // 没收的定金金额，单位：分
}

type PreorderCancelled struct {
	OrderID   uint   `json:"order_id"`
	OrderNum  uint64 `json:"order_num"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Refund    int64  `json:"refund"` // 退还金额，单位：分
}
