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
