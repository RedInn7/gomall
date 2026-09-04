package clearing

import (
	"time"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

const (
	ChannelWallet = "wallet"
	ChannelStripe = "stripe"
	ChannelWeb3   = "web3"
	ChannelXcash  = "xcash"

	StatusCleared  = "cleared"
	StatusSettled  = "settled"
	StatusRefunded = "refunded"
)

// PaymentClearing 记录普通订单从支付清算到履约后结算的资金生命周期。
//
// 支付成功只代表钱已经进入 merchant_escrow，不代表卖家已经可以支配这笔钱；
// order.completed 到达后，结算消费者才把 NetCents 从托管账户转入卖家钱包。
type PaymentClearing struct {
	dbmodel.Model
	OrderID     uint      `gorm:"not null;uniqueIndex"`
	BuyerID     uint      `gorm:"not null;index"`
	SellerID    uint      `gorm:"not null;index"`
	Channel     string    `gorm:"size:16;not null;index"`
	ProviderRef string    `gorm:"size:128;index"`
	GrossCents  int64     `gorm:"not null"`
	FeeCents    int64     `gorm:"not null;default:0"`
	NetCents    int64     `gorm:"not null"`
	Currency    string    `gorm:"size:8;not null"`
	Status      string    `gorm:"size:16;not null;index"`
	ClearedAt   time.Time `gorm:"not null"`
	SettledAt   *time.Time
	RefundedAt  *time.Time
}

func (PaymentClearing) TableName() string { return "payment_clearing" }

const (
	AnomalyReasonDuplicatePayment       = "duplicate_external_payment"
	AnomalyReasonAmountMismatch         = "amount_currency_mismatch"
	AnomalyReasonPaymentDetailsMismatch = "payment_details_mismatch"
	AnomalyReasonHighRiskPayment        = "high_risk_payment"
	AnomalyStatusPendingReview          = "pending_review"
	AnomalyStatusReviewing              = "reviewing"
	AnomalyStatusResolved               = "resolved"
	AnomalyStatusRefunded               = "refunded"
	AnomalyStatusRejected               = "rejected"
)

// PaymentAnomaly 保存外部渠道已经实收、但因为重复支付、金额或付款细节不符、
// 风险过高等原因不能进入正常清算的异常。记录进入待审核/退款队列，
// provider_ref 唯一保证事件重放不重复建单。
type PaymentAnomaly struct {
	dbmodel.Model
	OrderID        uint      `gorm:"not null;index"`
	Channel        string    `gorm:"size:16;not null;uniqueIndex:uniq_payment_anomaly_provider,priority:1"`
	ProviderRef    string    `gorm:"size:128;not null;uniqueIndex:uniq_payment_anomaly_provider,priority:2"`
	ProviderAmount string    `gorm:"size:128;not null"`
	Currency       string    `gorm:"size:16;not null"`
	Reason         string    `gorm:"size:64;not null;index"`
	Status         string    `gorm:"size:24;not null;index"`
	OccurredAt     time.Time `gorm:"not null"`
	LastOperatorID uint      `gorm:"index"`
	LastActionAt   *time.Time
	LastNote       string `gorm:"size:1024"`
	// ExternalRefundRef 只记录运营已在外部渠道执行退款后取得的凭证；
	// 修改异常单状态本身不会发起任何资金划转。
	ExternalRefundRef string `gorm:"size:128"`
	ResolvedAt        *time.Time
}

func (PaymentAnomaly) TableName() string { return "payment_anomaly" }

// PaymentAnomalyTransition 是异常款状态变化的不可变审计记录。
// 每次状态变化与 PaymentAnomaly 的条件更新处于同一个数据库事务。
type PaymentAnomalyTransition struct {
	dbmodel.Model
	AnomalyID         uint      `gorm:"not null;index"`
	FromStatus        string    `gorm:"size:24;not null"`
	ToStatus          string    `gorm:"size:24;not null"`
	OperatorID        uint      `gorm:"not null;index"`
	Note              string    `gorm:"size:1024;not null"`
	ExternalRefundRef string    `gorm:"size:128"`
	ActedAt           time.Time `gorm:"not null;index"`
}

func (PaymentAnomalyTransition) TableName() string { return "payment_anomaly_transition" }
