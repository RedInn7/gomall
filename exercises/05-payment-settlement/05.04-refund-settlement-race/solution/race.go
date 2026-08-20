//go:build exercise

package settlementrace

import (
	"errors"
	"math"
	"sync"
)

const (
	IntentSettle   = "settle"
	IntentRefund   = "refund"
	StatusCleared  = "cleared"
	StatusSettled  = "settled"
	StatusRefunded = "refunded"
)

var (
	ErrInvalidInput     = errors.New("invalid settlement input")
	ErrTerminalConflict = errors.New("clearing already reached another terminal state")
	ErrLedgerWrite      = errors.New("ledger write failed")
)

type Clearing struct {
	OrderID, BuyerID, SellerID uint
	GrossCents, NetCents       int64
	Status                     string
}
type Entry struct {
	OrderID, UserID    uint
	Account, Direction string
	Cents              int64
}
type Store struct {
	mu                            sync.Mutex
	records                       map[uint]Clearing
	sellerBalances, buyerBalances map[uint]int64
	entries                       []Entry
	failSecondLedger              bool
}

func NewStore(r Clearing, sellerBalance, buyerBalance int64) *Store {
	return &Store{records: map[uint]Clearing{r.OrderID: r}, sellerBalances: map[uint]int64{r.SellerID: sellerBalance}, buyerBalances: map[uint]int64{r.BuyerID: buyerBalance}}
}
func (s *Store) FailSecondLedgerWrite(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failSecondLedger = fail
}
func (s *Store) Snapshot(id uint) (Clearing, int64, int64, []Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[id]
	return r, s.sellerBalances[r.SellerID], s.buyerBalances[r.BuyerID], append([]Entry(nil), s.entries...)
}

func Apply(s *Store, orderID uint, intent string) error {
	if s == nil || orderID == 0 || (intent != IntentSettle && intent != IntentRefund) {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[orderID]
	if !ok || r.BuyerID == 0 || r.SellerID == 0 || r.GrossCents <= 0 || r.NetCents <= 0 {
		return ErrInvalidInput
	}
	want := StatusSettled
	if intent == IntentRefund {
		want = StatusRefunded
	}
	if r.Status == want {
		return nil
	}
	if r.Status == StatusSettled || r.Status == StatusRefunded {
		return ErrTerminalConflict
	}
	if r.Status != StatusCleared {
		return ErrInvalidInput
	}

	oldSeller, oldBuyer, oldLen := s.sellerBalances[r.SellerID], s.buyerBalances[r.BuyerID], len(s.entries)
	amount, userID, account := r.NetCents, r.SellerID, "seller_wallet"
	if intent == IntentRefund {
		amount, userID, account = r.GrossCents, r.BuyerID, "buyer_wallet"
		if s.buyerBalances[userID] > math.MaxInt64-amount {
			return ErrInvalidInput
		}
		s.buyerBalances[userID] += amount
	} else {
		if s.sellerBalances[userID] > math.MaxInt64-amount {
			return ErrInvalidInput
		}
		s.sellerBalances[userID] += amount
	}
	s.entries = append(s.entries, Entry{OrderID: orderID, Account: "merchant_escrow", Direction: "debit", Cents: amount})
	if s.failSecondLedger {
		s.sellerBalances[r.SellerID], s.buyerBalances[r.BuyerID], s.entries = oldSeller, oldBuyer, s.entries[:oldLen]
		return ErrLedgerWrite
	}
	s.entries = append(s.entries, Entry{OrderID: orderID, UserID: userID, Account: account, Direction: "credit", Cents: amount})
	r.Status = want
	s.records[orderID] = r
	return nil
}
