package redpacket

// MaxRedPacketTotalCents 单个红包总额上限（分），参照主流社交红包单个 200 元的上限。
// 封住单笔发包的资金敞口，拆包金额也远离 int64 溢出。
const MaxRedPacketTotalCents int64 = 200 * 100

type RedPacketCreateReq struct {
	Total     int64  `json:"total" form:"total" binding:"required,min=1"`          // 总金额，单位：分
	Count     int    `json:"count" form:"count" binding:"required,min=1,max=1000"` // 份数
	ExpireSec int    `json:"expire_sec" form:"expire_sec"`                         // 过期秒数，缺省 24h
	Greeting  string `json:"greeting" form:"greeting" binding:"max=128"`           // 与列 size:128 对齐，进事务前就拦
	Key       string `json:"key" form:"key" binding:"required"`                    // 支付密码，与 paydown 同名
}

type RedPacketClaimReq struct {
	ID   uint   `json:"id" form:"id" binding:"required"`
	Code string `json:"code" form:"code"` // 领取口令
}

type RedPacketShowReq struct {
	ID   uint   `json:"id" form:"id" binding:"required"`
	Code string `json:"code" form:"code"` // 发包人和领取人不用带
}

type RedPacketReconcileReq struct {
	ID uint `json:"id" form:"id" binding:"required"`
}

type RedPacketListReq struct {
	LastID   uint `json:"last_id" form:"last_id"`
	PageSize int  `json:"page_size" form:"page_size"`
}

type RedPacketResp struct {
	ID        uint   `json:"id"`
	UserID    uint   `json:"user_id"`
	Total     int64  `json:"total"`
	Count     int    `json:"count"`
	Remaining int    `json:"remaining"`
	ExpireAt  int64  `json:"expire_at"`
	Status    uint   `json:"status"`
	Greeting  string `json:"greeting,omitempty"`
	ShareCode string `json:"share_code,omitempty"` // 只回给发包人
}

type RedPacketClaimResp struct {
	RedPacketID uint  `json:"red_packet_id"`
	Amount      int64 `json:"amount"`
}

type RedPacketClaimItem struct {
	UserID uint  `json:"user_id"`
	Amount int64 `json:"amount"`
}

type RedPacketDetailResp struct {
	RedPacket *RedPacketResp        `json:"red_packet"`
	Claims    []*RedPacketClaimItem `json:"claims"`
}

type RedPacketListResp struct {
	LastID uint             `json:"last_id"`
	List   []*RedPacketResp `json:"list"`
}

type RedPacketReceivedItem struct {
	ClaimID     uint   `json:"claim_id"`
	RedPacketID uint   `json:"red_packet_id"`
	SenderID    uint   `json:"sender_id"`
	Amount      int64  `json:"amount"`
	Greeting    string `json:"greeting,omitempty"`
	ClaimedAt   int64  `json:"claimed_at"`
}

type RedPacketReceivedResp struct {
	LastID uint                     `json:"last_id"`
	List   []*RedPacketReceivedItem `json:"list"`
}

type RedPacketReconcileResp struct {
	Repaired int  `json:"repaired"` // 补领的孤儿领取数
	Refunded bool `json:"refunded"` // 份额池丢失，已提前退款
}
