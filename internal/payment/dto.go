package payment

type PaymentDownReq struct {
	OrderId uint   `form:"order_id" json:"order_id" binding:"required"`
	Key     string `form:"key" json:"key" binding:"required"`
}

// PayDownResp 余额支付成功返回体。当前成功路径不回传字段，data 恒为 null，
// 仅用于给 PayDown 一个具体返回类型以替换 interface{}。
type PayDownResp struct{}

// CryptoNonceReq 申请一次性签名 nonce，用 query 传 order_id
type CryptoNonceReq struct {
	OrderId uint `form:"order_id" json:"order_id" binding:"required"`
}

// CryptoNonceResp 返回 nonce 和供钱包签名的明文模板
type CryptoNonceResp struct {
	Nonce         string `json:"nonce"`
	MessageToSign string `json:"message_to_sign"`
	ExpiresIn     int    `json:"expires_in"`
}

// CryptoPaydownReq 钱包签名后的支付凭据。signature 走 0x 前缀十六进制
type CryptoPaydownReq struct {
	OrderID    uint   `json:"orderID" binding:"required"`
	WalletAddr string `json:"walletAddr" binding:"required"`
	Signature  string `json:"signature" binding:"required"`
	Nonce      string `json:"nonce" binding:"required"`
	ChainID    uint64 `json:"chainID" binding:"required"`
}

// CryptoPaydownResp 返回 pending 状态，提示用户去钱包发起转账
type CryptoPaydownResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// StripeCheckoutReq 发起 Stripe 托管支付，只传 order_id，金额由服务端按订单 FinalCents 计算。
type StripeCheckoutReq struct {
	OrderID uint `form:"order_id" json:"order_id" binding:"required"`
}

// StripeCheckoutResp 返回 Stripe 托管支付页地址，客户端跳转完成支付，结算由 webhook 兜底。
type StripeCheckoutResp struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
}

// XcashCheckoutReq 只接受订单号；金额、币种和链均由服务端订单与 Xcash 项目决定。
type XcashCheckoutReq struct {
	OrderID uint `form:"order_id" json:"order_id" binding:"required"`
}

// XcashCheckoutResp 是前端渲染 Xcash 支付页和付款进度所需的稳定字段。
type XcashCheckoutResp struct {
	SysNo                 string `json:"sys_no"`
	URL                   string `json:"url"`
	Amount                string `json:"amount"`
	Currency              string `json:"currency"`
	Status                string `json:"status"`
	ExpiresAt             string `json:"expires_at"`
	Chain                 string `json:"chain,omitempty"`
	Crypto                string `json:"crypto,omitempty"`
	PayAddress            string `json:"pay_address,omitempty"`
	PayAmount             string `json:"pay_amount,omitempty"`
	PaymentURI            string `json:"payment_uri,omitempty"`
	TxHash                string `json:"tx_hash,omitempty"`
	RiskLevel             string `json:"risk_level,omitempty"`
	RiskScore             string `json:"risk_score,omitempty"`
	Confirmations         uint64 `json:"confirmations"`
	RequiredConfirmations uint64 `json:"required_confirmations"`
	ConfirmProgress       int    `json:"confirm_progress"`
}
