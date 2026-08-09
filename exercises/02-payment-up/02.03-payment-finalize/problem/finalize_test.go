//go:build exercise

package paymentfinalize

import (
	"errors"
	"testing"
)

func TestFinalizePaymentCommitsStateAndEvent(t *testing.T) {
	db := NewDB()
	db.Orders[8] = Order{ID: 8, State: WaitPay}
	if err := FinalizePayment(db, 8, "balance", "evt-8"); err != nil {
		t.Fatal(err)
	}
	if got := db.Orders[8]; got.State != Paid || got.Channel != "balance" {
		t.Fatalf("order = %+v", got)
	}
	if got := db.Events["evt-8"]; got.Topic != "payment.succeeded" || got.OrderID != 8 {
		t.Fatalf("event = %+v", got)
	}
}

func TestFinalizePaymentRejectsStateRace(t *testing.T) {
	db := NewDB()
	db.Orders[8] = Order{ID: 8, State: Paid, Channel: "stripe"}
	if err := FinalizePayment(db, 8, "balance", "evt-8"); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("error = %v", err)
	}
	if len(db.Events) != 0 || db.Orders[8].Channel != "stripe" {
		t.Fatalf("partial overwrite: %+v %+v", db.Orders, db.Events)
	}
}

func TestFinalizePaymentRollsBackWhenOutboxFails(t *testing.T) {
	db := NewDB()
	db.Orders[8] = Order{ID: 8, State: WaitPay}
	db.FailOutbox = true
	if err := FinalizePayment(db, 8, "balance", "evt-8"); !errors.Is(err, ErrOutbox) {
		t.Fatalf("error = %v", err)
	}
	if got := db.Orders[8]; got.State != WaitPay || got.Channel != "" {
		t.Fatalf("order partially committed: %+v", got)
	}
	if len(db.Events) != 0 {
		t.Fatalf("event partially committed: %+v", db.Events)
	}
}
