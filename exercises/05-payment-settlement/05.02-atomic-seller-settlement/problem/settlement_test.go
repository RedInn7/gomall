//go:build exercise

package atomicsettlement

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func fixture() *Store {
	return NewStore(10_000, map[uint]int64{9: 700}, Clearing{41, 9, 1_200, StatusCleared})
}

func TestSettleMovesBalanceAndWritesBalancedEntries(t *testing.T) {
	s := fixture()
	if err := s.Settle(41); err != nil {
		t.Fatal(err)
	}
	if s.EscrowBalance != 8_800 || s.SellerBalances[9] != 1_900 || s.Clearings[41].Status != StatusSettled {
		t.Fatalf("store = %+v", s)
	}
	want := []Entry{{41, 0, AccountEscrow, DirectionDebit, 1200, 8800}, {41, 9, AccountSeller, DirectionCredit, 1200, 1900}}
	if !reflect.DeepEqual(s.Entries, want) {
		t.Fatalf("entries = %+v, want %+v", s.Entries, want)
	}
}

func TestSettleIsIdempotent(t *testing.T) {
	s := fixture()
	_ = s.Settle(41)
	snapshot := append([]Entry(nil), s.Entries...)
	if err := s.Settle(41); err != nil {
		t.Fatal(err)
	}
	if s.SellerBalances[9] != 1900 || !reflect.DeepEqual(s.Entries, snapshot) {
		t.Fatal("retry changed money or entries")
	}
}
func TestSettleRollsBackAfterDebitFailure(t *testing.T) {
	s := fixture()
	s.FailAfterDebit = true
	err := s.Settle(41)
	if !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("err=%v", err)
	}
	if s.EscrowBalance != 10000 || s.SellerBalances[9] != 700 || len(s.Entries) != 0 || s.Clearings[41].Status != StatusCleared {
		t.Fatalf("partial write: %+v", s)
	}
}
func TestSettleRejectsInsufficientEscrowWithoutWrites(t *testing.T) {
	s := fixture()
	s.EscrowBalance = 1199
	err := s.Settle(41)
	if !errors.Is(err, ErrInsufficientEscrow) {
		t.Fatalf("err=%v", err)
	}
	if s.SellerBalances[9] != 700 || len(s.Entries) != 0 || s.Clearings[41].Status != StatusCleared {
		t.Fatal("rejection wrote state")
	}
}
func TestSettleRejectsUnknownState(t *testing.T) {
	s := fixture()
	r := s.Clearings[41]
	r.Status = "refunded"
	s.Clearings[41] = r
	if err := s.Settle(41); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("err=%v", err)
	}
}
func TestSettleRejectsOverflowWithoutWrites(t *testing.T) {
	s := fixture()
	s.SellerBalances[9] = math.MaxInt64 - 1000
	if err := s.Settle(41); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v", err)
	}
	if s.EscrowBalance != 10000 || len(s.Entries) != 0 {
		t.Fatal("overflow wrote state")
	}
}
func TestSettleRejectsMissingSellerWithoutCreatingAccount(t *testing.T) {
	s := fixture()
	delete(s.SellerBalances, 9)
	if err := s.Settle(41); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v", err)
	}
	if _, ok := s.SellerBalances[9]; ok {
		t.Fatal("settlement created a missing seller account")
	}
	if s.EscrowBalance != 10000 || len(s.Entries) != 0 || s.Clearings[41].Status != StatusCleared {
		t.Fatal("missing seller changed settlement state")
	}
}
func TestSettleRejectsMalformedRecords(t *testing.T) {
	tests := []Clearing{{41, 0, 1200, StatusCleared}, {41, 9, 0, StatusCleared}, {99, 9, 1200, StatusCleared}}
	for _, r := range tests {
		s := NewStore(10000, map[uint]int64{9: 700}, r)
		if err := s.Settle(41); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("record=%+v err=%v", r, err)
		}
	}
}
