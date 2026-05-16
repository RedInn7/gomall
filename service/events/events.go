package events

// 业务事件 payload。routing key 与类型一一对应:
//   order.created        -> OrderCreated
//   order.paid           -> OrderPaid
//   order.cancelled      -> OrderCancelled
//   product.changed      -> ProductChanged
//   web3.payment.pending -> Web3PaymentPending

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
