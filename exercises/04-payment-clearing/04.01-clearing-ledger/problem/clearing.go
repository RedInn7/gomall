//go:build exercise

package clearingledger

import (
	"errors"
	"strings"
)

const (
	ChannelWallet = "wallet"
	ChannelStripe = "stripe"
	ChannelWeb3   = "web3"

	AccountUserWallet       = "user_wallet"
	AccountExternalClearing = "external_clearing"
	AccountMerchantEscrow   = "merchant_escrow"

	DirectionDebit    = "debit"
	DirectionCredit   = "credit"
	StatusCleared     = "cleared"
	BizTypeOrderClear = "order_clear"
)

var (
	ErrInvalidClearingInput = errors.New("invalid clearing input")
	ErrDuplicateOrder       = errors.New("order already cleared")
	ErrEscrowCreditFailed   = errors.New("merchant escrow credit failed")
)

// Order 只保留计算应付金额和确定交易双方所需的字段。
type Order struct {
	ID          uint
	BuyerID     uint
	SellerID    uint
	Num         int
	Money       int64
	PromoRuleID uint
	FinalCents  int64
}

// ClearingRecord 是“支付已实收”的业务凭证，不代表卖家已经结算到账。
type ClearingRecord struct {
	OrderID     uint
	BuyerID     uint
	SellerID    uint
	Channel     string
	ProviderRef string
	GrossCents  int64
	FeeCents    int64
	NetCents    int64
	Currency    string
	Status      string
}

// LedgerEntry 是清算阶段的一条账本分录。
type LedgerEntry struct {
	OrderID           uint
	UserID            uint
	AccountCode       string
	Direction         string
	AmountCents       int64
	BalanceAfterCents int64
	BizType           string
}

// Store 模拟同一数据库事务中的清算单和账本表。
type Store struct {
	records          map[uint]ClearingRecord
	entries          []LedgerEntry
	failEscrowCredit bool
}

func NewStore() *Store {
	return &Store{records: make(map[uint]ClearingRecord)}
}

// FailEscrowCredit 模拟第二条 merchant_escrow 分录写入失败。
func (s *Store) FailEscrowCredit(fail bool) {
	s.failEscrowCredit = fail
}

func (s *Store) Clearing(orderID uint) (ClearingRecord, bool) {
	if s == nil {
		return ClearingRecord{}, false
	}
	record, ok := s.records[orderID]
	return record, ok
}

// Entries 返回副本，避免调用者绕过清算事务修改账本。
func (s *Store) Entries() []LedgerEntry {
	if s == nil {
		return nil
	}
	return append([]LedgerEntry(nil), s.entries...)
}

// RecordClearedTx 记录一张清算单，以及金额相等的一借一贷两条分录。
func RecordClearedTx(
	store *Store,
	o *Order,
	channel, providerRef, currency string,
	walletBalanceAfter *int64,
) error {
	// TODO 1：校验 store、订单主键、交易双方、数量、渠道和币种。
	// TODO 2：Wallet 必须提供扣款后余额；Stripe/Web3 不能提供该余额。
	// TODO 3：普通订单使用 Money * Num，促销订单使用 FinalCents；拒绝负数金额。
	// TODO 4：同一 OrderID 只能清算一次；清算单应满足 FeeCents=0、NetCents=GrossCents。
	// TODO 5：标准化 providerRef 与 currency，并完整保存订单、买卖双方和渠道。
	// TODO 6：Wallet 借记 user_wallet，外部渠道借记 external_clearing。
	// TODO 7：统一贷记 merchant_escrow；任一步失败都不能留下清算单或单边账。
	_ = strings.TrimSpace
	return nil
}
