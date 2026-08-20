//go:build exercise

package concurrentsettlement

import (
	"errors"
	"sync"
)

const (
	StatusCleared = "cleared"
	StatusSettled = "settled"
)

var (
	ErrUnknownOrder = errors.New("unknown order")
	ErrInvalidState = errors.New("invalid state")
)

type Clearing struct {
	OrderID, SellerID uint
	Amount            int64
	Status            string
}
type Entry struct {
	OrderID, SellerID uint
	Amount            int64
}
type Snapshot struct {
	Escrow         int64
	SellerBalances map[uint]int64
	Clearings      map[uint]Clearing
	Entries        []Entry
}
type Store struct {
	mu        sync.Mutex
	escrow    int64
	sellers   map[uint]int64
	clearings map[uint]Clearing
	entries   []Entry
}

func NewStore(escrow int64, records ...Clearing) *Store {
	c := make(map[uint]Clearing, len(records))
	for _, r := range records {
		c[r.OrderID] = r
	}
	return &Store{escrow: escrow, sellers: map[uint]int64{}, clearings: c}
}
func (s *Store) Settle(orderID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.clearings[orderID]
	if !ok {
		return ErrUnknownOrder
	}
	if r.Status == StatusSettled {
		return nil
	}
	if r.Status != StatusCleared || r.Amount <= 0 || r.SellerID == 0 || s.escrow < r.Amount {
		return ErrInvalidState
	}
	s.escrow -= r.Amount
	s.sellers[r.SellerID] += r.Amount
	s.entries = append(s.entries, Entry{r.OrderID, r.SellerID, r.Amount})
	r.Status = StatusSettled
	s.clearings[orderID] = r
	return nil
}
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := make(map[uint]int64, len(s.sellers))
	for k, v := range s.sellers {
		b[k] = v
	}
	c := make(map[uint]Clearing, len(s.clearings))
	for k, v := range s.clearings {
		c[k] = v
	}
	return Snapshot{s.escrow, b, c, append([]Entry(nil), s.entries...)}
}
