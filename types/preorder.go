package types

// PreorderShowResp 预售配置展示。所有时间用 unix 秒，前端按时区渲染。
type PreorderShowResp struct {
	ProductID      uint  `json:"product_id"`
	DepositCents   int64 `json:"deposit_cents"`
	FinalCents     int64 `json:"final_cents"`
	TotalCents     int64 `json:"total_cents"`
	DepositStartAt int64 `json:"deposit_start_at"`
	DepositEndAt   int64 `json:"deposit_end_at"`
	FinalEndAt     int64 `json:"final_end_at"`
	ShipAt         int64 `json:"ship_at"`
	NowAt          int64 `json:"now_at"` // 服务端时间，避免客户端时钟偏移误判窗口
	Phase          string `json:"phase"` // deposit / final / forfeited / not_started
}

// PreorderDepositReq 付定金。Key 用于解密 / 重加密 user.money，沿用 paydown 口径。
// AddressID 在定金期可填可不填；尾款支付前补地址在前端引导即可。
type PreorderDepositReq struct {
	ProductID uint   `uri:"productID" json:"product_id"`
	BossID    uint   `json:"boss_id" binding:"required"`
	AddressID uint   `json:"address_id"`
	Key       string `json:"key" binding:"required"`
}

// PreorderFinalReq 付尾款。
type PreorderFinalReq struct {
	OrderID uint   `uri:"orderID" json:"order_id"`
	Key     string `json:"key" binding:"required"`
}

// PreorderCancelReq 定金期内取消，全额退款。
type PreorderCancelReq struct {
	OrderID uint   `uri:"orderID" json:"order_id"`
	Key     string `json:"key" binding:"required"`
}

// PreorderActionResp 通用操作回包，三个写接口共用。
type PreorderActionResp struct {
	OrderID       uint   `json:"order_id"`
	OrderNum      uint64 `json:"order_num"`
	PreorderStage int    `json:"preorder_stage"`
	OrderType     uint   `json:"order_type"`
	Message       string `json:"message"`
}
