package model

import (
	"time"

	"github.com/jinzhu/gorm"
)

const (
	CouponTypeAmount   = 1 // 满减
	CouponTypeDiscount = 2 // 折扣（百分比）

	UserCouponStatusUnused  = 1
	UserCouponStatusUsed    = 2
	UserCouponStatusExpired = 3
)

// CouponBatch 优惠券批次/活动
type CouponBatch struct {
	gorm.Model
	Name       string    `gorm:"size:128;not null"`
	Type       int       `gorm:"not null;default:1"`        // 1 满减 / 2 折扣
	Threshold  int64     `gorm:"not null"`                  // 满 X 分可用
	Amount     int64     `gorm:"not null"`                  // 满减: 减 X 分；折扣: 百分比 (例如 85 = 85 折)
	Total      int64     `gorm:"not null"`                  // 总发行量
	Claimed    int64     `gorm:"not null;default:0"`        // DB 中已领数（最终一致）
	PerUser    int64     `gorm:"not null;default:1"`        // 每用户上限
	StartAt    time.Time `gorm:"not null"`
	EndAt      time.Time `gorm:"not null"`
	ValidDays  int       `gorm:"not null;default:7"`        // 领取后有效天数
}

func (CouponBatch) TableName() string { return "coupon_batch" }

// UserCoupon 用户领取的具体优惠券
type UserCoupon struct {
	gorm.Model
	UserId       uint      `gorm:"not null;index"`
	BatchId      uint      `gorm:"not null;index"`
	Code         string    `gorm:"size:32;uniqueIndex"`
	Status       int       `gorm:"not null;default:1"` // 1 未使用 / 2 已使用 / 3 已过期
	UsedOrderId  uint      `gorm:"default:0"`
	ClaimedAt    time.Time `gorm:"not null"`
	UsedAt       *time.Time
	ExpireAt     time.Time `gorm:"not null;index"`
}

func (UserCoupon) TableName() string { return "user_coupon" }
