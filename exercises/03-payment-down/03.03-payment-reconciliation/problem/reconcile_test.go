//go:build exercise

package reconciliation

import (
	"reflect"
	"testing"
)

func TestReconcileFindsPaymentAnomaliesInOrder(t *testing.T) {
	orders := []Order{{ID: 3, State: "paid", Channel: "stripe", PaidCents: 300}, {ID: 1, State: "paid", Channel: "balance", PaidCents: 100}, {ID: 2, State: "paid", Channel: "balance", PaidCents: 200}, {ID: 4, State: "wait_pay", Channel: "", PaidCents: 0}}
	entries := []Entry{
		{OrderID: 1, Direction: "debit", Channel: "balance", Cents: 100}, {OrderID: 1, Direction: "credit", Channel: "balance", Cents: 100},
		{OrderID: 2, Direction: "debit", Channel: "balance", Cents: 200},
		{OrderID: 3, Direction: "debit", Channel: "web3", Cents: 300}, {OrderID: 3, Direction: "credit", Channel: "web3", Cents: 300},
	}
	want := []Issue{{OrderID: 2, Code: "missing_entries"}, {OrderID: 3, Code: "channel_mismatch"}}
	if got := Reconcile(orders, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("issues = %+v, want %+v", got, want)
	}
}

func TestReconcileDetectsUnbalancedPair(t *testing.T) {
	orders := []Order{{ID: 7, State: "paid", Channel: "balance", PaidCents: 500}}
	entries := []Entry{{OrderID: 7, Direction: "debit", Channel: "balance", Cents: 500}, {OrderID: 7, Direction: "credit", Channel: "balance", Cents: 450}}
	want := []Issue{{OrderID: 7, Code: "unbalanced"}}
	if got := Reconcile(orders, entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("issues = %+v, want %+v", got, want)
	}
}
