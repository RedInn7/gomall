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
	// TODO: 调用 db.Transaction，在同一个事务中完成下面两次写入：
	// 1. 使用 tx.InsertOrder 写入 order；
	// 2. 使用 tx.InsertOutbox 写入 ID 为 eventID、Topic 为 "order.created"、
	//    Aggregate 为 order.ID 的事件，并把错误原样返回。
	// Outbox 写入失败时，Transaction 必须让订单和事件一起回滚。
	return nil
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
