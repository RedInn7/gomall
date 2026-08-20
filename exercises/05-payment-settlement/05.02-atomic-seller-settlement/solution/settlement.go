//go:build exercise

package atomicsettlement

import (
	"errors"
	"math"
)

const (
	StatusCleared   = "cleared"
	StatusSettled   = "settled"
	DirectionDebit  = "debit"
	DirectionCredit = "credit"
	AccountEscrow   = "merchant_escrow"
	AccountSeller   = "seller_wallet"
)

var (
	ErrInvalidInput       = errors.New("invalid settlement input")
	ErrInvalidState       = errors.New("invalid clearing state")
	ErrInsufficientEscrow = errors.New("insufficient escrow")
	ErrInjectedFailure    = errors.New("injected ledger failure")
)

type Clearing struct {
	OrderID, SellerID uint
	NetCents          int64
	Status            string
}
type Entry struct {
	OrderID, UserID                uint
	Account, Direction             string
	AmountCents, BalanceAfterCents int64
}
type Store struct {
	SellerBalances map[uint]int64
	EscrowBalance  int64
	Clearings      map[uint]Clearing
	Entries        []Entry
	FailAfterDebit bool
}

func NewStore(escrow int64, balances map[uint]int64, records ...Clearing) *Store {
	b := make(map[uint]int64, len(balances))
	for id, v := range balances {
		b[id] = v
	}
	c := make(map[uint]Clearing, len(records))
	for _, r := range records {
		c[r.OrderID] = r
	}
	return &Store{SellerBalances: b, EscrowBalance: escrow, Clearings: c}
}

func (s *Store) Settle(orderID uint) error {
	if s == nil || orderID == 0 || s.SellerBalances == nil || s.Clearings == nil {
		return ErrInvalidInput
	}
	r, ok := s.Clearings[orderID]
	if !ok || r.OrderID != orderID || r.SellerID == 0 || r.NetCents <= 0 {
		return ErrInvalidInput
	}
	if r.Status == StatusSettled {
		return nil
	}
	if r.Status != StatusCleared {
		return ErrInvalidState
	}
	if s.EscrowBalance < r.NetCents {
		return ErrInsufficientEscrow
	}
	before, sellerExists := s.SellerBalances[r.SellerID]
	if !sellerExists {
		return ErrInvalidInput
	}
	if before > math.MaxInt64-r.NetCents {
		return ErrInvalidInput
	}
	after := before + r.NetCents

	oldEscrow, oldEntries := s.EscrowBalance, len(s.Entries)
	s.EscrowBalance -= r.NetCents
	s.SellerBalances[r.SellerID] = after
	s.Entries = append(s.Entries, Entry{orderID, 0, AccountEscrow, DirectionDebit, r.NetCents, s.EscrowBalance})
	if s.FailAfterDebit {
		s.EscrowBalance = oldEscrow
		s.SellerBalances[r.SellerID] = before
		s.Entries = s.Entries[:oldEntries]
		return ErrInjectedFailure
	}
	s.Entries = append(s.Entries, Entry{orderID, r.SellerID, AccountSeller, DirectionCredit, r.NetCents, after})
	r.Status = StatusSettled
	s.Clearings[orderID] = r
	return nil
}
