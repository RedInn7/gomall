package order

import (
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

// Order 订单信息
type Order struct {
	dbmodel.Model
	UserID    uint `gorm:"not null;index:idx_order_user_type,priority:1"`
	ProductID uint `gorm:"not null"`
	BossID    uint `gorm:"not null"`
	AddressID uint `gorm:"not null"`
	// ShipTo 下单时的收货地址快照（address_name / address_phone / address_detail）。
	// 地址表可改可删，订单展示和发货只认快照。
	ShipTo   address.Snapshot `gorm:"embedded;embeddedPrefix:address_"`
	Num      int              // 数量
	OrderNum uint64           // 订单号
	Type     uint             `gorm:"index:idx_order_user_type,priority:2"` // 状态机参见 consts.OrderXxx (1 待付 / 2 待发 / 3 关闭 / 4 待收 / 5 完成 / 6 退款中 / 7 已退)
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

	// ---- 履约 ----
	// TrackingNo / Carrier 发货时必填并落库，用户查物流、客诉对账都以这里为准。
	TrackingNo string `gorm:"size:64;not null;default:''"`
	Carrier    string `gorm:"size:32;not null;default:''"`
	// ShippedAt 发货时间，自动确认收货从这里计时。不能用 updated_at：退款申请 / 驳回都会刷新它，
	// 买家反复申请退款就能无限推迟放款。
	ShippedAt *time.Time `gorm:"index"`
	// CompletedAt 确认收货时间，已完成订单的售后时效从这里计时。
	CompletedAt *time.Time

	// ---- 售后 ----
	// RefundReason 最近一次退款申请的原因，商家审批时要看。
	RefundReason string `gorm:"size:255;not null;default:''"`
	// RefundCount 已申请退款次数，超过上限不再受理，防止反复申请骚扰商家。
	RefundCount int `gorm:"not null;default:0"`
	// RestockedAt 退款后回补库存的时间，nil = 还没回补。未发货的退款在结算时回补；
	// 已发货的要等商家确认退货入库才回补，货还在买家手里就回补会超卖。
	RestockedAt *time.Time

	// Ticket 异步下单的排队凭证，唯一索引保证一个 ticket 只建一张单；同步下单为 NULL。
	Ticket *string `gorm:"size:32;uniqueIndex"`
}

// BackfillFulfillmentColumns 给新增的履约 / 售后列回填历史数据，迁移时在 AutoMigrate 之后调用，可重复执行。
//   - shipped_at / completed_at：历史订单没有，用 updated_at 近似，否则老的待收货单永远不会被自动确认、
//     老的已完成单拿不到售后时效起点；
//   - 地址快照：从地址表拷一次，已被删掉的地址只能留空；
//   - restocked_at：旧逻辑在退款结算时一律回补过库存，已结算的退款单标成已回补，避免商家确认退货时再补一次。
func BackfillFulfillmentColumns(db *gorm.DB) error {
	shipped := []uint{consts.OrderWaitReceive, consts.OrderCompleted}
	steps := []*gorm.DB{
		db.Model(&Order{}).
			Where("shipped_at IS NULL AND (type IN ? OR (type=? AND refund_from_type IN ?))", shipped, consts.OrderRefunding, shipped).
			UpdateColumn("shipped_at", gorm.Expr("updated_at")),
		db.Model(&Order{}).
			Where("completed_at IS NULL AND (type=? OR (type=? AND refund_from_type=?))", consts.OrderCompleted, consts.OrderRefunding, consts.OrderCompleted).
			UpdateColumn("completed_at", gorm.Expr("updated_at")),
		db.Model(&Order{}).
			Where("address_name='' AND address_id IN (SELECT id FROM address)").
			UpdateColumns(map[string]interface{}{
				"address_name":   gorm.Expr("(SELECT name FROM address WHERE address.id=`order`.address_id)"),
				"address_phone":  gorm.Expr("(SELECT phone FROM address WHERE address.id=`order`.address_id)"),
				"address_detail": gorm.Expr("(SELECT address FROM address WHERE address.id=`order`.address_id)"),
			}),
		db.Model(&Order{}).
			Where("restocked_at IS NULL AND type=? AND EXISTS (SELECT 1 FROM account_transaction WHERE ref_order_id=`order`.id AND direction=? AND biz_type IN ?)",
				consts.OrderRefunded, money.DirectionCredit, []string{money.BizTypeRefund, money.BizTypeEscrowRefund}).
			UpdateColumn("restocked_at", gorm.Expr("updated_at")),
	}
	for _, s := range steps {
		if s.Error != nil {
			return s.Error
		}
	}
	return nil
}
