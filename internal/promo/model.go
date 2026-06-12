package promo

import (
	"time"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

// 满减 / 阶梯折扣规则。与 CouponBatch 互补：
//   - Coupon 需要用户领取，PerUser 限领；
//   - PromoRule 不需要领取，结算时引擎自动取最优；
//   - 一笔订单只能匹配一条 PromoRule（不叠加），但 PromoRule 可与已领取的 Coupon 叠加 ——
//     业务规则：先满减、后券。引擎只负责前半段。
//
// 金额字段一律使用 int64（单位：分），避免浮点累计误差。
const (
	PromoRuleTypeAmount   = 1 // 满 X 减 Y
	PromoRuleTypeDiscount = 2 // 满 X 打折（按 bps，9000 = 9 折）

	PromoScopeAll      = 1 // 全场
	PromoScopeCategory = 2 // 类目级
	PromoScopeProduct  = 3 // 商品级

	PromoStatusDraft   = 0 // 草稿（运营在配置中）
	PromoStatusActive  = 1 // 已生效
	PromoStatusStopped = 2 // 已停用

	// PromoDiscountBpsBase 折扣分母。9 折 = 9000，bps = 折后金额 / base
	PromoDiscountBpsBase = 10000
)

type PromoRule struct {
	dbmodel.Model
	Name             string    `gorm:"size:128;not null"`        // 业务可读名，会回传给前端 / 客服
	RuleType         int       `gorm:"not null;default:1"`       // 1 满减 / 2 满折扣
	Scope            int       `gorm:"not null;default:1;index"` // 1 全场 / 2 类目 / 3 商品
	ScopeRefID       int64     `gorm:"not null;default:0;index"` // 类目 id 或商品 id；全场 = 0
	ThresholdCents   int64     `gorm:"not null"`                 // 满 X 分
	DiscountCents    int64     `gorm:"not null;default:0"`       // 减 Y 分（RuleType=1）
	DiscountBps      int       `gorm:"not null;default:0"`       // 折扣 bps（RuleType=2），9 折 = 9000
	DailyBudgetCents int64     `gorm:"not null;default:0"`       // 当日预算分；0 = 不限
	ConsumedToday    int64     `gorm:"not null;default:0"`       // 当日已消耗分（每日 0 点重置 —— cron 路线图）
	StartAt          time.Time `gorm:"not null;index:idx_promo_window"`
	EndAt            time.Time `gorm:"not null;index:idx_promo_window"`
	Status           int       `gorm:"not null;default:0;index"` // 0 draft / 1 active / 2 stopped
}

func (PromoRule) TableName() string { return "promo_rule" }

// PromoRelease 预算退还台账。order_id 唯一索引承担幂等去重：
// order.cancelled / order.refunded 事件 at-least-once 投递下重复消费时，
// INSERT 冲突即判定"已释放过"，不会二次回补预算。
// 一笔订单整个生命周期最多退还一次（取消与退款互斥），所以唯一键落在 order_id 上。
type PromoRelease struct {
	dbmodel.Model
	OrderID       uint   `gorm:"uniqueIndex"`
	RuleID        uint   `gorm:"not null;index"`
	DiscountCents int64  `gorm:"not null"`
	Reason        string `gorm:"size:32"` // cancel / refund / manual
}

func (PromoRelease) TableName() string { return "promo_release" }
