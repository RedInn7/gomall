//go:build exercise

package createordersaga

import "errors"

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
	// TODO 1：校验依赖、订单号、用户、商品、正数数量、初始状态 OrderWaitPay 和非空 eventID。
	// TODO 2：预先检查已存在的订单与事件；完全相同的重放直接成功，任何复用冲突返回对应错误，且不得再次预占。
	// TODO 3：先调用 inv.reserve；失败时不写订单和事件。
	// TODO 4：在一个 store.transaction 中写入订单和 Topic 为 order.created 的事件；模拟写入失败时返回给定错误。
	// TODO 5：事务失败后调用 inv.release 补偿。释放也失败时用 errors.Join 同时保留事务错误和 ErrReleaseFailed。
	// TODO 6：成功后必须满足库存只从 Available 移到 Reserved，且订单与事件同时可见。
	_ = errors.Join
	return nil
}
