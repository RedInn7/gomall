//go:build exercise

package concurrentsettlement

import (
	"errors"
	"sync"
	"testing"
)

func runConcurrent(t *testing.T, n int, fn func() error) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) { defer wg.Done(); <-start; errs[i] = fn() }(i)
	}
	close(start)
	wg.Wait()
	return errs
}

func TestConcurrentRetriesPayExactlyOnce(t *testing.T) {
	s := NewStore(10_000, Clearing{77, 9, 1_250, StatusCleared})
	for i, err := range runConcurrent(t, 64, func() error { return s.Settle(77) }) {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	got := s.Snapshot()
	if got.Escrow != 8_750 || got.SellerBalances[9] != 1_250 || len(got.Entries) != 1 || got.Clearings[77].Status != StatusSettled {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestDifferentOrdersToSameSellerDoNotLoseUpdates(t *testing.T) {
	const n = 80
	records := make([]Clearing, 0, n)
	for i := 1; i <= n; i++ {
		records = append(records, Clearing{uint(i), 42, 10, StatusCleared})
	}
	s := NewStore(10_000, records...)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 1; i <= n; i++ {
		go func(id uint) {
			defer wg.Done()
			<-start
			if err := s.Settle(id); err != nil {
				t.Errorf("order %d: %v", id, err)
			}
		}(uint(i))
	}
	close(start)
	wg.Wait()
	got := s.Snapshot()
	if got.Escrow != 9_200 || got.SellerBalances[42] != 800 || len(got.Entries) != 80 {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestUnknownAndInvalidRecordsDoNotWrite(t *testing.T) {
	s := NewStore(100, Clearing{1, 7, -1, StatusCleared}, Clearing{2, 7, 20, "refunded"})
	if err := s.Settle(99); !errors.Is(err, ErrUnknownOrder) {
		t.Fatalf("err=%v", err)
	}
	if err := s.Settle(1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("err=%v", err)
	}
	if err := s.Settle(2); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("err=%v", err)
	}
	got := s.Snapshot()
	if got.Escrow != 100 || len(got.Entries) != 0 || len(got.SellerBalances) != 0 {
		t.Fatalf("snapshot=%+v", got)
	}
}
func TestInsufficientEscrowDoesNotPartiallySettle(t *testing.T) {
	s := NewStore(99, Clearing{1, 7, 100, StatusCleared})
	if err := s.Settle(1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("err=%v", err)
	}
	got := s.Snapshot()
	if got.Escrow != 99 || len(got.Entries) != 0 || got.Clearings[1].Status != StatusCleared {
		t.Fatalf("snapshot=%+v", got)
	}
}
