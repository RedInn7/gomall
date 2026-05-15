package types

import "time"

type CouponBatchCreateReq struct {
	Name      string    `json:"name" binding:"required"`
	Type      int       `json:"type" binding:"required,oneof=1 2"`
	Threshold int64     `json:"threshold"`
	Amount    int64     `json:"amount" binding:"required"`
	Total     int64     `json:"total" binding:"required,min=1"`
	PerUser   int64     `json:"per_user" binding:"required,min=1"`
	StartAt   time.Time `json:"start_at" binding:"required"`
	EndAt     time.Time `json:"end_at" binding:"required"`
	ValidDays int       `json:"valid_days" binding:"required,min=1"`
}

type CouponClaimReq struct {
	BatchId uint `json:"batch_id" form:"batch_id" binding:"required"`
	// Mode: "redis" 走 Lua 原子扣减；"db" 走 SELECT FOR UPDATE
	Mode string `json:"mode" form:"mode"`
}

type CouponListReq struct {
	Status int `json:"status" form:"status"` // 0 全部 / 1 未使用 / 2 已使用 / 3 已过期
}
