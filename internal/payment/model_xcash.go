package payment

import (
	"time"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

const (
	XcashIntentWaiting   = "waiting"
	XcashIntentCompleted = "completed"
	XcashIntentExpired   = "expired"
	XcashIntentAnomaly   = "anomaly"
)

// XcashPaymentIntent 保存 Gomall 订单和 Xcash 账单之间的映射。
// 一个订单在旧账单过期后可以创建下一 attempt，但同时只会向用户返回最新未过期账单。
type XcashPaymentIntent struct {
	dbmodel.Model
	OrderID               uint   `gorm:"not null;uniqueIndex:uniq_xcash_order_attempt,priority:1;index"`
	UserID                uint   `gorm:"not null;index"`
	Attempt               uint   `gorm:"not null;uniqueIndex:uniq_xcash_order_attempt,priority:2"`
	OutNo                 string `gorm:"size:32;not null;uniqueIndex"`
	SysNo                 string `gorm:"size:32;not null;uniqueIndex"`
	AmountCents           int64  `gorm:"not null"`
	Currency              string `gorm:"size:8;not null"`
	Status                string `gorm:"size:24;not null;index"`
	Chain                 string `gorm:"size:32"`
	Crypto                string `gorm:"size:32"`
	CryptoAddress         string `gorm:"size:128"`
	PayAddress            string `gorm:"size:128"`
	PayAmount             string `gorm:"size:128"`
	PayURL                string `gorm:"size:512;not null"`
	PaymentURI            string `gorm:"size:1024"`
	TxHash                string `gorm:"size:128;index"`
	RiskLevel             string `gorm:"size:32"`
	RiskScore             string `gorm:"size:32"`
	ObservedChain         string `gorm:"size:32"`
	ObservedCrypto        string `gorm:"size:32"`
	ObservedPayAddress    string `gorm:"size:128"`
	ObservedPayAmount     string `gorm:"size:128"`
	Confirmations         uint64
	RequiredConfirmations uint64
	ConfirmProgress       int
	ExpiresAt             time.Time `gorm:"not null;index"`
	ConfirmedAt           *time.Time
}

func (XcashPaymentIntent) TableName() string { return "xcash_payment_intent" }

// XcashWebhookReceipt 与订单结算写在同一事务中。唯一 nonce 吸收 Xcash 重投；
// 若结算失败，事务回滚后同 nonce 仍可再次处理。
type XcashWebhookReceipt struct {
	dbmodel.Model
	AppID       string    `gorm:"size:64;not null;uniqueIndex:uniq_xcash_webhook_nonce,priority:1"`
	Nonce       string    `gorm:"size:128;not null;uniqueIndex:uniq_xcash_webhook_nonce,priority:2"`
	EventType   string    `gorm:"size:32;not null"`
	ProviderRef string    `gorm:"size:192;not null;index"`
	ProcessedAt time.Time `gorm:"not null"`
}

func (XcashWebhookReceipt) TableName() string { return "xcash_webhook_receipt" }
