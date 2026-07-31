//go:build exercise

package transactionaloutbox

import (
	"errors"
	"testing"
)

func TestCreateOrderCommitsOrderAndEventTogether(t *testing.T) {
	db := NewDB()
	order := Order{ID: 17, UserID: 3}
	if err := CreateOrder(db, order, "evt-17"); err != nil {
		t.Fatal(err)
	}
	if got, ok := db.Orders[17]; !ok || got != order {
		t.Fatalf("order not committed: %+v", db.Orders)
	}
	wantEvent := Event{ID: "evt-17", Topic: "order.created", Aggregate: 17}
	if got, ok := db.Events["evt-17"]; !ok || got != wantEvent {
		t.Fatalf("event = %+v, want %+v", got, wantEvent)
	}
}

func TestCreateOrderRollsBackWhenOutboxFails(t *testing.T) {
	db := NewDB()
	db.FailOutbox = true
	err := CreateOrder(db, Order{ID: 18, UserID: 4}, "evt-18")
	if !errors.Is(err, ErrOutboxUnavailable) {
		t.Fatalf("error = %v, want %v", err, ErrOutboxUnavailable)
	}
	if len(db.Orders) != 0 || len(db.Events) != 0 {
		t.Fatalf("partial commit: orders=%+v events=%+v", db.Orders, db.Events)
	}
}
