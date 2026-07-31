//go:build exercise

package transactionaloutbox

import "errors"

var ErrOutboxUnavailable = errors.New("outbox unavailable")

type Order struct {
	ID     uint
	UserID uint
}

type Event struct {
	ID        string
	Topic     string
	Aggregate uint
}

type DB struct {
	Orders     map[uint]Order
	Events     map[string]Event
	FailOutbox bool
}

type Tx struct {
	orders     map[uint]Order
	events     map[string]Event
	failOutbox bool
}

func NewDB() *DB {
	return &DB{Orders: map[uint]Order{}, Events: map[string]Event{}}

}

func (db *DB) Transaction(fn func(*Tx) error) error {
	tx := &Tx{
		orders:     cloneOrders(db.Orders),
		events:     cloneEvents(db.Events),
		failOutbox: db.FailOutbox,
	}
	if err := fn(tx); err != nil {
		return err
	}
	db.Orders = tx.orders
	db.Events = tx.events
	return nil
}

func (tx *Tx) InsertOrder(order Order) {
	tx.orders[order.ID] = order
}

func (tx *Tx) InsertOutbox(event Event) error {
	if tx.failOutbox {
		return ErrOutboxUnavailable
	}
	tx.events[event.ID] = event
	return nil
}

func CreateOrder(db *DB, order Order, eventID string) error {
	return db.Transaction(func(tx *Tx) error {
		tx.InsertOrder(order)
		return tx.InsertOutbox(Event{
			ID:        eventID,
			Topic:     "order.created",
			Aggregate: order.ID,
		})
	})
}

func cloneOrders(src map[uint]Order) map[uint]Order {
	dst := make(map[uint]Order, len(src))
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
