package order

import (
	"time"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

// Order 订单信息
type Order struct {
	dbmodel.Model
	UserID    uint   `gorm:"not null;index:idx_order_user_type,priority:1"`
	ProductID uint   `gorm:"not null"`
	BossID    uint   `gorm:"not null"`
	AddressID uint   `gorm:"not null"`
	Num       int    // 数量
	OrderNum  uint64 // 订单号
	Type      uint   `gorm:"index:idx_order_user_type,priority:2"` // 状态机参见 consts.OrderXxx (1 待付 / 2 待发 / 3 关闭 / 4 待收 / 5 完成 / 6 退款中 / 7 已退)
	// RefundFromType 记录进入 Refunding 前的状态，驳回退款时必须恢复原状态，不能一律跳到 Completed。
	RefundFromType uint  `gorm:"not null;default:0"`
	Money          int64 // 单位：分。单价口径（订单结算时下游可能再 * Num）；预售订单写定金 + 尾款累计金额
	// ---- 预售两段式支付字段 ----
	// PreorderStage: 0=普通订单 / 1=已付定金 / 2=已付尾款 / 3=定金没收 (参 model.PreorderStageXxx)
	// 非预售订单恒为 0，旧消费者无需感知。
	PreorderStage int        `gorm:"not null;default:0;index"`
	DepositPaidAt *time.Time // 定金到账时间，nil = 未付
	FinalPaidAt   *time.Time // 尾款到账时间，nil = 未付

	// ---- 满减引擎落点 ----
	// PromoRuleID    本次下单命中的 promo_rule.id；0 = 没有命中 / 预算耗尽降级
	// PromoDiscountCents 命中规则减免的金额（分）；下游可对 final_cents 校验
	// FinalCents     满减结算后用户实付金额（分）= Money*Num - PromoDiscountCents，clamp >= 0；
	//                老客户端不读这个字段时，仍可由 Money*Num 兜底
	PromoRuleID        uint  `gorm:"not null;default:0;index"`
	PromoDiscountCents int64 `gorm:"not null;default:0"`
	FinalCents         int64 `gorm:"not null;default:0"`

	// StockCommitPending 支付已落库，但 Redis 预占核销（CommitReservation）重试后仍失败。
	// 库存对账把这份预占当合法占用并补核销，不会当泄漏退回 available（那样会超卖）。
	StockCommitPending bool `gorm:"not null;default:false;index"`

	// Ticket 异步下单的排队凭证，唯一索引保证一个 ticket 只建一张单；同步下单为 NULL。
	Ticket *string `gorm:"size:32;uniqueIndex"`
}
