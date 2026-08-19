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

type Order struct {
	ID          uint
	BuyerID     uint
	SellerID    uint
	Num         int
	Money       int64
	PromoRuleID uint
	FinalCents  int64
}

type ClearingRecord struct {
	OrderID     uint
	BuyerID     uint
	SellerID    uint
	Channel     string
	ProviderRef string
	GrossCents  int64
	Currency    string
	Status      string
}

type LedgerEntry struct {
	OrderID           uint
	UserID            uint
	AccountCode       string
	Direction         string
	AmountCents       int64
	BalanceAfterCents int64
	BizType           string
}

type Store struct {
	records          map[uint]ClearingRecord
	entries          []LedgerEntry
	failEscrowCredit bool
}

func NewStore() *Store {
	return &Store{records: make(map[uint]ClearingRecord)}
}

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

func (s *Store) Entries() []LedgerEntry {
	if s == nil {
		return nil
	}
	return append([]LedgerEntry(nil), s.entries...)
}

func RecordClearedTx(
	store *Store,
	o *Order,
	channel, providerRef, currency string,
	walletBalanceAfter *int64,
) error {
	if store == nil || o == nil || o.ID == 0 || o.BuyerID == 0 || o.SellerID == 0 || o.Num <= 0 {
		return ErrInvalidClearingInput
	}
	if channel != ChannelWallet && channel != ChannelStripe && channel != ChannelWeb3 {
		return ErrInvalidClearingInput
	}
	if strings.TrimSpace(currency) == "" || (channel == ChannelWallet) != (walletBalanceAfter != nil) {
		return ErrInvalidClearingInput
	}

	gross := o.Money * int64(o.Num)
	if o.PromoRuleID != 0 {
		gross = o.FinalCents
	}
	if gross < 0 {
		return ErrInvalidClearingInput
	}
	if _, exists := store.records[o.ID]; exists {
		return ErrDuplicateOrder
	}

	record := ClearingRecord{
		OrderID:     o.ID,
		BuyerID:     o.BuyerID,
		SellerID:    o.SellerID,
		Channel:     channel,
		ProviderRef: strings.TrimSpace(providerRef),
		GrossCents:  gross,
		Currency:    strings.ToUpper(strings.TrimSpace(currency)),
		Status:      StatusCleared,
	}

	debit := LedgerEntry{
		OrderID:     o.ID,
		Direction:   DirectionDebit,
		AmountCents: gross,
		BizType:     BizTypeOrderClear,
	}
	if channel == ChannelWallet {
		debit.UserID = o.BuyerID
		debit.AccountCode = AccountUserWallet
		debit.BalanceAfterCents = *walletBalanceAfter
	} else {
		debit.AccountCode = AccountExternalClearing
	}

	credit := LedgerEntry{
		OrderID:     o.ID,
		AccountCode: AccountMerchantEscrow,
		Direction:   DirectionCredit,
		AmountCents: gross,
		BizType:     BizTypeOrderClear,
	}
	if store.failEscrowCredit {
		return ErrEscrowCreditFailed
	}

	// 所有步骤都成功后再统一提交，模拟数据库事务的原子提交。
	store.records[o.ID] = record
	store.entries = append(store.entries, debit, credit)
	return nil
}
