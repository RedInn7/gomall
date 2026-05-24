package model

import (
	"time"

	"github.com/jinzhu/gorm"
)

// Order 订单信息
type Order struct {
	gorm.Model
	UserID    uint   `gorm:"not null"`
	ProductID uint   `gorm:"not null"`
	BossID    uint   `gorm:"not null"`
	AddressID uint   `gorm:"not null"`
	Num       int    // 数量
	OrderNum  uint64 // 订单号
	Type      uint   // 状态机参见 consts.OrderXxx (1 待付 / 2 待发 / 3 关闭 / 4 待收 / 5 完成 / 6 退款中 / 7 已退)
	Money     int64  // 单位：分。预售订单 = 定金 + 尾款累计金额
	// ---- 预售两段式支付字段 ----
	// PreorderStage: 0=普通订单 / 1=已付定金 / 2=已付尾款 / 3=定金没收 (参 model.PreorderStageXxx)
	// 非预售订单恒为 0，旧消费者无需感知。
	PreorderStage int        `gorm:"not null;default:0;index"`
	DepositPaidAt *time.Time // 定金到账时间，nil = 未付
	FinalPaidAt   *time.Time // 尾款到账时间，nil = 未付
}
