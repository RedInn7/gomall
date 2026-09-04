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
	AnomalyReasonDuplicatePayment = "duplicate_external_payment"
	AnomalyReasonAmountMismatch   = "amount_currency_mismatch"
	AnomalyStatusPendingReview    = "pending_review"
)

// PaymentAnomaly 保存“订单已经由另一笔支付完成，但外部渠道又确实收了一笔钱”的异常。
// 这类资金不能当成幂等重放直接吞掉；记录进入待审核/退款队列，provider_ref 唯一保证事件重放不重复建单。
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
}

func (PaymentAnomaly) TableName() string { return "payment_anomaly" }
