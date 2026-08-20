//go:build exercise

package settlementrace

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func baseStore() *Store {
	return NewStore(Clearing{OrderID: 9, BuyerID: 11, SellerID: 22, GrossCents: 1_000, NetCents: 940, Status: StatusCleared}, 200, 300)
}

func TestConcurrentRefundAndSettlementHaveOneWinner(t *testing.T) {
	for round := 0; round < 100; round++ {
		store := baseStore()
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, intent := range []string{IntentSettle, IntentRefund} {
			wg.Add(1)
			go func(intent string) { defer wg.Done(); <-start; errs <- Apply(store, 9, intent) }(intent)
		}
		close(start)
		wg.Wait()
		close(errs)
		var successes, conflicts int
		for err := range errs {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrTerminalConflict):
				conflicts++
			default:
				t.Fatalf("unexpected error: %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
		}
		r, seller, buyer, entries := store.Snapshot(9)
		if len(entries) != 2 {
			t.Fatalf("entries=%+v", entries)
		}
		switch r.Status {
		case StatusSettled:
			if seller != 1_140 || buyer != 300 || entries[1].Account != "seller_wallet" {
				t.Fatalf("bad settled snapshot: %+v %d %d %+v", r, seller, buyer, entries)
			}
		case StatusRefunded:
			if seller != 200 || buyer != 1_300 || entries[1].Account != "buyer_wallet" {
				t.Fatalf("bad refunded snapshot: %+v %d %d %+v", r, seller, buyer, entries)
			}
		default:
			t.Fatalf("status=%q", r.Status)
		}
	}
}

func TestReplayIsIdempotentAndOppositeIntentConflicts(t *testing.T) {
	store := baseStore()
	if err := Apply(store, 9, IntentSettle); err != nil {
		t.Fatal(err)
	}
	if err := Apply(store, 9, IntentSettle); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := Apply(store, 9, IntentRefund); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("opposite error=%v", err)
	}
	r, seller, buyer, entries := store.Snapshot(9)
	if r.Status != StatusSettled || seller != 1_140 || buyer != 300 || len(entries) != 2 {
		t.Fatalf("snapshot=%+v %d %d %+v", r, seller, buyer, entries)
	}
}

func TestSecondLedgerFailureRollsBackEverything(t *testing.T) {
	store := baseStore()
	store.FailSecondLedgerWrite(true)
	if err := Apply(store, 9, IntentRefund); !errors.Is(err, ErrLedgerWrite) {
		t.Fatalf("error=%v", err)
	}
	r, seller, buyer, entries := store.Snapshot(9)
	if r.Status != StatusCleared || seller != 200 || buyer != 300 || len(entries) != 0 {
		t.Fatalf("partial write: %+v %d %d %+v", r, seller, buyer, entries)
	}
}

func TestApplyRejectsInvalidInputWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		store  *Store
		id     uint
		intent string
	}{
		{"nil store", nil, 9, IntentSettle}, {"zero id", baseStore(), 0, IntentSettle}, {"unknown intent", baseStore(), 9, "chargeback"}, {"missing order", baseStore(), 99, IntentSettle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Apply(tc.store, tc.id, tc.intent); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
func TestApplyRejectsZeroAmountsAndOverflowWithoutWrites(t *testing.T) {
	for _, intent := range []string{IntentSettle, IntentRefund} {
		s := baseStore()
		r := s.records[9]
		if intent == IntentSettle {
			r.NetCents = 0
		} else {
			r.GrossCents = 0
		}
		s.records[9] = r
		if err := Apply(s, 9, intent); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("intent=%s zero amount err=%v", intent, err)
		}
	}

	for _, intent := range []string{IntentSettle, IntentRefund} {
		s := baseStore()
		if intent == IntentSettle {
			s.sellerBalances[22] = math.MaxInt64 - 100
		} else {
			s.buyerBalances[11] = math.MaxInt64 - 100
		}
		if err := Apply(s, 9, intent); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("intent=%s overflow err=%v", intent, err)
		}
		r, sellerBalance, buyerBalance, entries := s.Snapshot(9)
		if r.Status != StatusCleared || len(entries) != 0 {
			t.Fatalf("intent=%s changed state: record=%+v entries=%+v", intent, r, entries)
		}
		if intent == IntentSettle && (sellerBalance != math.MaxInt64-100 || buyerBalance != 300) {
			t.Fatalf("settlement overflow changed balances: seller=%d buyer=%d", sellerBalance, buyerBalance)
		}
		if intent == IntentRefund && (sellerBalance != 200 || buyerBalance != math.MaxInt64-100) {
			t.Fatalf("refund overflow changed balances: seller=%d buyer=%d", sellerBalance, buyerBalance)
		}
	}
}
