//go:build exercise

package createordersaga

import (
	"errors"
	"strings"
)

var (
	ErrInvalidOrder      = errors.New("invalid order")
	ErrInsufficientStock = errors.New("insufficient stock")
	ErrOrderConflict     = errors.New("order conflict")
	ErrEventConflict     = errors.New("event conflict")
	ErrOrderWrite        = errors.New("order write failed")
	ErrOutboxWrite       = errors.New("outbox write failed")
	ErrReleaseFailed     = errors.New("release failed")
)

type Order struct {
	Number            string
	UserID, ProductID uint
	Num               int
	State             string
}
type Event struct{ ID, Topic, OrderNumber string }
type Inventory struct {
	Available, Reserved        map[uint]int
	FailReserve, FailRelease   bool
	ReserveCalls, ReleaseCalls int
}
type Store struct {
	Orders                map[string]Order
	Events                map[string]Event
	FailOrder, FailOutbox bool
}
type tx struct {
	orders                map[string]Order
	events                map[string]Event
	failOrder, failOutbox bool
}

func NewInventory(productID uint, available int) *Inventory {
	return &Inventory{Available: map[uint]int{productID: available}, Reserved: map[uint]int{}}
}
func NewStore() *Store { return &Store{Orders: map[string]Order{}, Events: map[string]Event{}} }
func (i *Inventory) reserve(productID uint, num int) error {
	i.ReserveCalls++
	if i.FailReserve || num > i.Available[productID] {
		return ErrInsufficientStock
	}
	i.Available[productID] -= num
	i.Reserved[productID] += num
	return nil
}
func (i *Inventory) release(productID uint, num int) error {
	i.ReleaseCalls++
	if i.FailRelease || num > i.Reserved[productID] {
		return ErrReleaseFailed
	}
	i.Reserved[productID] -= num
	i.Available[productID] += num
	return nil
}
func (s *Store) transaction(fn func(*tx) error) error {
	t := &tx{orders: cloneOrders(s.Orders), events: cloneEvents(s.Events), failOrder: s.FailOrder, failOutbox: s.FailOutbox}
	if err := fn(t); err != nil {
		return err
	}
	s.Orders = t.orders
	s.Events = t.events
	return nil
}
func cloneOrders(src map[string]Order) map[string]Order {
	dst := make(map[string]Order, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func cloneEvents(src map[string]Event) map[string]Event {
	dst := make(map[string]Event, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func CreateOrderSaga(inv *Inventory, store *Store, order Order, eventID string) error {
	eventID = strings.TrimSpace(eventID)
	order.Number = strings.TrimSpace(order.Number)
	if inv == nil || store == nil || order.Number == "" || eventID == "" || order.UserID == 0 || order.ProductID == 0 || order.Num <= 0 || order.State != "OrderWaitPay" {
		return ErrInvalidOrder
	}
	event := Event{ID: eventID, Topic: "order.created", OrderNumber: order.Number}
	if old, ok := store.Orders[order.Number]; ok {
		if old != order {
			return ErrOrderConflict
		}
		if e, ok := store.Events[eventID]; ok && e == event {
			return nil
		}
		return ErrEventConflict
	}
	if e, ok := store.Events[eventID]; ok {
		if e != event {
			return ErrEventConflict
		}
		return ErrOrderConflict
	}
	if err := inv.reserve(order.ProductID, order.Num); err != nil {
		return err
	}
	err := store.transaction(func(t *tx) error {
		if t.failOrder {
			return ErrOrderWrite
		}
		t.orders[order.Number] = order
		if t.failOutbox {
			return ErrOutboxWrite
		}
		t.events[eventID] = event
		return nil
	})
	if err == nil {
		return nil
	}
	if releaseErr := inv.release(order.ProductID, order.Num); releaseErr != nil {
		return errors.Join(err, releaseErr)
	}
	return err
}
