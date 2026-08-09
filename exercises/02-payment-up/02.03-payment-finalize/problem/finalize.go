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
	// TODO: 在 Orders 和 Events 的副本上完成支付，全部成功后再一次性替换 db 中的数据。
	// 1. 订单不存在返回 ErrOrderNotFound；状态不是 WaitPay 返回 ErrStateChanged；
	// 2. 把订单状态改为 Paid，并记录 channel；
	// 3. db.FailOutbox 为 true 时返回 ErrOutbox，不得提交订单修改；
	// 4. 写入 ID 为 eventID、Topic 为 "payment.succeeded"、OrderID 为 orderID 的事件；
	// 5. 最后同时提交订单和事件副本，保证失败时没有半完成状态。
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
