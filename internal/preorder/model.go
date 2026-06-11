package preorder

import (
	"time"

	"github.com/jinzhu/gorm"
)

// ProductPreorder 预售商品配置。
// 一个商品要么不预售（无此行）、要么有且仅有一行 preorder 记录。
// 定金 / 尾款金额单位均为"分"，与 order.money / user.money 口径对齐。
//
// 时间窗口划分：
//
//	[DepositStartAt, DepositEndAt)   定金期，PayDeposit 在此区间可调用
//	[DepositEndAt,   FinalEndAt)     尾款期，PayFinal 在此区间可调用
//	[FinalEndAt,     +∞)             失效期，未付尾款由 cron 没收
//	ShipAt                           预计发货时间，仅展示，不参与状态机
type ProductPreorder struct {
	gorm.Model
	ProductID      uint      `gorm:"not null;uniqueIndex"`
	DepositCents   int64     `gorm:"not null"`
	FinalCents     int64     `gorm:"not null"`
	DepositStartAt time.Time `gorm:"not null"`
	DepositEndAt   time.Time `gorm:"not null"`
	FinalEndAt     time.Time `gorm:"not null"`
	ShipAt         time.Time
}

func (ProductPreorder) TableName() string {
	return "product_preorder"
}

// 预售订单阶段。沿用 order.preorder_stage 字段，0 = 非预售路径。
// 一旦订单进入 PreorderStageDepositPaid，order.type 仍为 OrderWaitPay 直到尾款成功；
// 这样兼容存量数据：旧消费者只看 order.type 不感知预售阶段。
const (
	PreorderStageNone        = 0 // 普通订单
	PreorderStageDepositPaid = 1 // 已付定金
	PreorderStageFinalPaid   = 2 // 已付尾款
	PreorderStageForfeited   = 3 // 定金没收（尾款逾期）
)
