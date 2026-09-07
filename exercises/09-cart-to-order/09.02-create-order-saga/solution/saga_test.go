//go:build exercise

package createordersaga

import (
	"errors"
	"reflect"
	"testing"
)

func validOrder() Order {
	return Order{Number: "ord-1", UserID: 10, ProductID: 7, Num: 2, State: "OrderWaitPay"}
}

func TestCreateOrderSagaSuccess(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	if err := CreateOrderSaga(inv, store, validOrder(), "evt-1"); err != nil {
		t.Fatal(err)
	}
	if inv.Available[7] != 3 || inv.Reserved[7] != 2 {
		t.Fatalf("inventory=%+v", inv)
	}
	if store.Orders["ord-1"] != validOrder() {
		t.Fatalf("orders=%+v", store.Orders)
	}
	if store.Events["evt-1"] != (Event{ID: "evt-1", Topic: "order.created", OrderNumber: "ord-1"}) {
		t.Fatalf("events=%+v", store.Events)
	}
}

func TestCreateOrderSagaValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Order)
		event  string
	}{
		{"blank number", func(o *Order) { o.Number = " " }, "evt"}, {"missing user", func(o *Order) { o.UserID = 0 }, "evt"},
		{"missing product", func(o *Order) { o.ProductID = 0 }, "evt"}, {"zero num", func(o *Order) { o.Num = 0 }, "evt"},
		{"negative num", func(o *Order) { o.Num = -1 }, "evt"}, {"wrong state", func(o *Order) { o.State = "Paid" }, "evt"},
		{"blank event", func(o *Order) {}, " 	"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			o := validOrder()
			tt.mutate(&o)
			inv, store := NewInventory(7, 5), NewStore()
			err := CreateOrderSaga(inv, store, o, tt.event)
			if !errors.Is(err, ErrInvalidOrder) || inv.ReserveCalls != 0 || len(store.Orders) != 0 || len(store.Events) != 0 {
				t.Fatalf("err=%v inv=%+v store=%+v", err, inv, store)
			}
		})
	}
}

func TestCreateOrderSagaNilDependencies(t *testing.T) {
	if !errors.Is(CreateOrderSaga(nil, NewStore(), validOrder(), "evt"), ErrInvalidOrder) {
		t.Fatal("nil inventory")
	}
	if !errors.Is(CreateOrderSaga(NewInventory(7, 5), nil, validOrder(), "evt"), ErrInvalidOrder) {
		t.Fatal("nil store")
	}
}

func TestInsufficientStockDoesNotWrite(t *testing.T) {
	inv, store := NewInventory(7, 1), NewStore()
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrInsufficientStock) || inv.Available[7] != 1 || inv.Reserved[7] != 0 || len(store.Orders) != 0 || len(store.Events) != 0 {
		t.Fatalf("err=%v inv=%+v store=%+v", err, inv, store)
	}
}

func TestReserveDependencyFailureDoesNotWrite(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	inv.FailReserve = true
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrInsufficientStock) || len(store.Orders) != 0 || len(store.Events) != 0 {
		t.Fatalf("err=%v store=%+v", err, store)
	}
}

func TestOrderWriteFailureCompensates(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	store.FailOrder = true
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrOrderWrite) || inv.Available[7] != 5 || inv.Reserved[7] != 0 || inv.ReleaseCalls != 1 || len(store.Orders) != 0 || len(store.Events) != 0 {
		t.Fatalf("err=%v inv=%+v store=%+v", err, inv, store)
	}
}

func TestOutboxFailureRollsBackAndCompensates(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	store.FailOutbox = true
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrOutboxWrite) || inv.Available[7] != 5 || inv.Reserved[7] != 0 || len(store.Orders) != 0 || len(store.Events) != 0 {
		t.Fatalf("err=%v inv=%+v store=%+v", err, inv, store)
	}
}

func TestCompensationFailurePreservesBothErrors(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	store.FailOutbox = true
	inv.FailRelease = true
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrOutboxWrite) || !errors.Is(err, ErrReleaseFailed) || inv.Available[7] != 3 || inv.Reserved[7] != 2 || len(store.Orders) != 0 {
		t.Fatalf("err=%v inv=%+v store=%+v", err, inv, store)
	}
}

func TestExactReplayIsIdempotent(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	if err := CreateOrderSaga(inv, store, validOrder(), " evt "); err != nil {
		t.Fatal(err)
	}
	beforeA, beforeR := inv.Available[7], inv.Reserved[7]
	store.FailOrder = true
	inv.FailReserve = true
	if err := CreateOrderSaga(inv, store, validOrder(), "evt"); err != nil {
		t.Fatalf("replay=%v", err)
	}
	if inv.ReserveCalls != 1 || inv.Available[7] != beforeA || inv.Reserved[7] != beforeR || len(store.Orders) != 1 || len(store.Events) != 1 {
		t.Fatalf("inv=%+v store=%+v", inv, store)
	}
}

func TestOrderNumberConflictDoesNotReserve(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	store.Orders["ord-1"] = Order{Number: "ord-1", UserID: 99, ProductID: 7, Num: 2, State: "OrderWaitPay"}
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrOrderConflict) || inv.ReserveCalls != 0 {
		t.Fatalf("err=%v inv=%+v", err, inv)
	}
}

func TestExistingOrderWithoutMatchingEventConflicts(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	store.Orders["ord-1"] = validOrder()
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrEventConflict) || inv.ReserveCalls != 0 {
		t.Fatalf("err=%v inv=%+v", err, inv)
	}
}

func TestEventIDConflictDoesNotReserve(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	store.Events["evt"] = Event{ID: "evt", Topic: "order.created", OrderNumber: "another"}
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrEventConflict) || inv.ReserveCalls != 0 {
		t.Fatalf("err=%v inv=%+v", err, inv)
	}
}

func TestExistingMatchingEventWithoutOrderIsConflict(t *testing.T) {
	inv, store := NewInventory(7, 5), NewStore()
	store.Events["evt"] = Event{ID: "evt", Topic: "order.created", OrderNumber: "ord-1"}
	before := cloneEvents(store.Events)
	err := CreateOrderSaga(inv, store, validOrder(), "evt")
	if !errors.Is(err, ErrOrderConflict) || inv.ReserveCalls != 0 || !reflect.DeepEqual(store.Events, before) {
		t.Fatalf("err=%v inv=%+v store=%+v", err, inv, store)
	}
}
