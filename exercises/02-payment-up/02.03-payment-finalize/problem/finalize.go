//go:build exercise

package paymentfinalize

import "errors"

var (
	ErrOrderNotFound = errors.New("order not found")
	ErrStateChanged  = errors.New("order state changed")
	ErrOutbox        = errors.New("outbox unavailable")
)

const (
	WaitPay = "wait_pay"
	Paid    = "paid"
)

type Order struct {
	ID             uint
	State, Channel string
}
type Event struct {
	ID, Topic string
	OrderID   uint
}
type DB struct {
	Orders     map[uint]Order
	Events     map[string]Event
	FailOutbox bool
}

func NewDB() *DB { return &DB{Orders: map[uint]Order{}, Events: map[string]Event{}} }

func FinalizePayment(db *DB, orderID uint, channel, eventID string) error {
	// TODO: 在事务副本中做状态条件更新并写 Outbox，全部成功后再提交。
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
