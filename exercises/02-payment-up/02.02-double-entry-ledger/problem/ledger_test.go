//go:build exercise

package doubleentry

import (
	"errors"
	"reflect"
	"testing"
)

func TestPostPaymentWritesBalancedPair(t *testing.T) {
	l := NewLedger()
	if err := l.PostPayment("order:42", 7, 9, 1250); err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{BizKey: "order:42", AccountID: 7, Direction: "debit", Cents: 1250},
		{BizKey: "order:42", AccountID: 9, Direction: "credit", Cents: 1250},
	}
	if got := l.Entries(); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
}

func TestPostPaymentRejectsDuplicateWithoutAppending(t *testing.T) {
	l := NewLedger()
	if err := l.PostPayment("order:42", 7, 9, 1250); err != nil {
		t.Fatal(err)
	}
	if err := l.PostPayment("order:42", 7, 9, 1250); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate error = %v, want %v", err, ErrDuplicate)
	}
	if got := len(l.Entries()); got != 2 {
		t.Fatalf("duplicate appended entries: got %d", got)
	}
}

func TestPostPaymentRejectsInvalidTransferWithoutPartialEntry(t *testing.T) {
	l := NewLedger()
	if err := l.PostPayment("bad", 7, 9, 0); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("amount error = %v, want %v", err, ErrInvalidAmount)
	}
	if err := l.PostPayment("same", 7, 7, 100); !errors.Is(err, ErrSameAccount) {
		t.Fatalf("account error = %v, want %v", err, ErrSameAccount)
	}
	if got := l.Entries(); len(got) != 0 {
		t.Fatalf("invalid transfer left entries: %+v", got)
	}
}
