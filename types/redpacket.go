package types

type RedPacketCreateReq struct {
	Total     int64  `json:"total" form:"total" binding:"required,min=1"`          // 总金额，单位：分
	Count     int    `json:"count" form:"count" binding:"required,min=1,max=1000"` // 份数
	ExpireSec int    `json:"expire_sec" form:"expire_sec"`                         // 过期秒数，缺省 24h
	Greeting  string `json:"greeting" form:"greeting"`
}

type RedPacketClaimReq struct {
	ID uint `json:"id" form:"id" binding:"required"`
}

type RedPacketShowReq struct {
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
