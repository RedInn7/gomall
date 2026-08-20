//go:build exercise

package settlementrace

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func baseStore() *Store {
	return NewStore(Clearing{OrderID: 9, BuyerID: 11, SellerID: 22, GrossCents: 1000, NetCents: 940, Status: StatusCleared}, 200, 300)
}
func TestConcurrentRefundAndSettlementHaveOneWinner(t *testing.T) {
	for n := 0; n < 100; n++ {
		s := baseStore()
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, in := range []string{IntentSettle, IntentRefund} {
			wg.Add(1)
			go func(in string) { defer wg.Done(); <-start; errs <- Apply(s, 9, in) }(in)
		}
		close(start)
		wg.Wait()
		close(errs)
		ok, conf := 0, 0
		for err := range errs {
			if err == nil {
				ok++
			} else if errors.Is(err, ErrTerminalConflict) {
				conf++
			} else {
				t.Fatal(err)
			}
		}
		if ok != 1 || conf != 1 {
			t.Fatalf("ok=%d conflict=%d", ok, conf)
		}
		r, se, b, e := s.Snapshot(9)
		if len(e) != 2 {
			t.Fatal(e)
		}
		if r.Status == StatusSettled {
			if se != 1140 || b != 300 || e[1].Account != "seller_wallet" {
				t.Fatal(r, se, b, e)
			}
		} else if r.Status == StatusRefunded {
			if se != 200 || b != 1300 || e[1].Account != "buyer_wallet" {
				t.Fatal(r, se, b, e)
			}
		} else {
			t.Fatal(r)
		}
	}
}
func TestReplayIsIdempotentAndOppositeIntentConflicts(t *testing.T) {
	s := baseStore()
	if err := Apply(s, 9, IntentSettle); err != nil {
		t.Fatal(err)
	}
	if err := Apply(s, 9, IntentSettle); err != nil {
		t.Fatal(err)
	}
	if err := Apply(s, 9, IntentRefund); !errors.Is(err, ErrTerminalConflict) {
		t.Fatal(err)
	}
	r, se, b, e := s.Snapshot(9)
	if r.Status != StatusSettled || se != 1140 || b != 300 || len(e) != 2 {
		t.Fatal(r, se, b, e)
	}
}
func TestSecondLedgerFailureRollsBackEverything(t *testing.T) {
	s := baseStore()
	s.FailSecondLedgerWrite(true)
	if err := Apply(s, 9, IntentRefund); !errors.Is(err, ErrLedgerWrite) {
		t.Fatal(err)
	}
	r, se, b, e := s.Snapshot(9)
	if r.Status != StatusCleared || se != 200 || b != 300 || len(e) != 0 {
		t.Fatal(r, se, b, e)
	}
}
func TestApplyRejectsInvalidInputWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		s  *Store
		id uint
		in string
	}{{nil, 9, IntentSettle}, {baseStore(), 0, IntentSettle}, {baseStore(), 9, "chargeback"}, {baseStore(), 99, IntentSettle}} {
		if err := Apply(tc.s, tc.id, tc.in); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
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
