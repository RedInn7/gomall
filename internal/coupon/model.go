package coupon

import (
	"errors"
	"time"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"
	"github.com/RedInn7/gomall/repository/cache"
)

const (
	CouponTypeAmount   = 1 // 满减
	CouponTypeDiscount = 2 // 折扣（百分比）

	UserCouponStatusUnused  = 1
	UserCouponStatusUsed    = 2
	UserCouponStatusExpired = 3
)

var (
	// ErrPerUserLimitReached 用户已达到该批次的领券上限。与 Redis 闸门返回的是同一个错误值，
	// Lua 路径、DB 落库守卫、db 模式三处都能用 errors.Is 统一判断。
	ErrPerUserLimitReached = cache.ErrCouponPerUserExceed
	// ErrCouponUnavailable 下单用券时券不存在、不属于当前用户、已使用或已过期。
	ErrCouponUnavailable = errors.New("优惠券不可用：不存在、已使用或已过期")
	// ErrCouponThresholdNotMet 满减后的金额没达到券的使用门槛。
	ErrCouponThresholdNotMet = errors.New("未达到优惠券使用门槛")
)

// ComputeDiscount 券在 base（满减之后的金额）上能减多少分；没达到门槛返回 0。
//   - 满减券：减 Amount，封顶到 base，不让实付变负
//   - 折扣券：Amount=85 表示 85 折，减 base*(100-Amount)/100，向下取整保平台
func ComputeDiscount(b *CouponBatch, base int64) int64 {
	if base <= 0 || base < b.Threshold {
		return 0
	}
	switch b.Type {
	case CouponTypeAmount:
		if b.Amount > base {
			return base
		}
		return b.Amount
	case CouponTypeDiscount:
		if b.Amount <= 0 || b.Amount >= 100 {
			return 0
		}
		return base * (100 - b.Amount) / 100
	}
	return 0
}

// CouponBatch 优惠券批次/活动
type CouponBatch struct {
	dbmodel.Model
	Name      string    `gorm:"size:128;not null"`
	Type      int       `gorm:"not null;default:1"` // 1 满减 / 2 折扣
	Threshold int64     `gorm:"not null"`           // 满 X 分可用
	Amount    int64     `gorm:"not null"`           // 满减: 减 X 分；折扣: 百分比 (例如 85 = 85 折)
	Total     int64     `gorm:"not null"`           // 总发行量
	Claimed   int64     `gorm:"not null;default:0"` // DB 中已领数（最终一致）
	PerUser   int64     `gorm:"not null;default:1"` // 每用户上限
	StartAt   time.Time `gorm:"not null"`
	EndAt     time.Time `gorm:"not null"`
	ValidDays int       `gorm:"not null;default:7"` // 领取后有效天数
}

func (CouponBatch) TableName() string { return "coupon_batch" }

// UserCoupon 用户领取的具体优惠券
type UserCoupon struct {
	dbmodel.Model
	UserId      uint      `gorm:"not null;index"`
	BatchId     uint      `gorm:"not null;index"`
	Code        string    `gorm:"size:32;uniqueIndex"`
	Status      int       `gorm:"not null;default:1"` // 1 未使用 / 2 已使用 / 3 已过期
	UsedOrderId uint      `gorm:"default:0"`
	ClaimedAt   time.Time `gorm:"not null"`
	UsedAt      *time.Time
	ExpireAt    time.Time `gorm:"not null;index"`
}

func (UserCoupon) TableName() string { return "user_coupon" }
