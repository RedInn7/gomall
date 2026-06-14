package money

import (
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

// 资金流水方向：借记（账户余额减少）/ 贷记（账户余额增加）。
const (
	DirectionDebit  = "debit"
	DirectionCredit = "credit"
)

// 流水业务类型，用于对账时区分资金来源。
const (
	BizTypeOrderPay  = "order_pay"
	BizTypeStripePay = "stripe_pay"
	BizTypeWeb3Pay   = "web3_pay"
)

// ExternalClearingUserID 平台对外资金清算账户的 user_id（0 = 系统账户）。
// Stripe / Web3 等外部资金入口，买家不从内部钱包扣款，复式记账的 debit 对手方记在该清算账户上，
// 保持 SUM(debit)=SUM(credit) 守恒。
const ExternalClearingUserID uint = 0

// StripeClearingUserID 保留兼容旧引用，语义同 ExternalClearingUserID。
const StripeClearingUserID = ExternalClearingUserID

// AccountTransaction 复式记账资金流水台账。
//
// 余额密文落库（users.money）不可对账，本表为每一次余额变动追加一条不可变流水：
// amount_cents / balance_after_cents 均为明文 int64 分，绝不加密，供对账与审计。
// 一次资金转移会成对出现（一方 debit、一方 credit），ref_order_id 关联订单。
//
// 幂等：对 (ref_order_id, direction) 建唯一索引，保证同一订单同方向只入账一次，
// 杜绝重复扣款 / 重复入账。
type AccountTransaction struct {
	dbmodel.Model
	UserID            uint   `gorm:"not null;index:idx_acct_tx_user"`
	Direction         string `gorm:"size:8;not null;uniqueIndex:uniq_acct_tx_order_dir,priority:2"`
	AmountCents       int64  `gorm:"not null"`
	RefOrderID        uint   `gorm:"not null;uniqueIndex:uniq_acct_tx_order_dir,priority:1"`
	BalanceAfterCents int64  `gorm:"not null"`
	BizType           string `gorm:"size:32;not null"`
}

func (AccountTransaction) TableName() string { return "account_transaction" }
