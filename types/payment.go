package types

type PaymentDownReq struct {
	OrderId uint   `form:"order_id" json:"order_id" binding:"required"`
	Key     string `form:"key" json:"key" binding:"required"`
}

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
