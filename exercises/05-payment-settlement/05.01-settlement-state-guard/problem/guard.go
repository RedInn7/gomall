//go:build exercise

package settlementguard

import "errors"

type OrderStatus string
type ClearingStatus string
type Decision string

const (
	OrderCreated   OrderStatus = "created"
	OrderPaid      OrderStatus = "paid"
	OrderCompleted OrderStatus = "completed"
	OrderRefunding OrderStatus = "refunding"
	OrderRefunded  OrderStatus = "refunded"

	ClearingCleared  ClearingStatus = "cleared"
	ClearingSettled  ClearingStatus = "settled"
	ClearingRefunded ClearingStatus = "refunded"

	DecisionSettle Decision = "settle"
	DecisionNoop   Decision = "noop"
)

var (
	ErrInvalidOrderID        = errors.New("invalid order id")
	ErrMissingClearing       = errors.New("missing clearing record")
	ErrClearingOrderMismatch = errors.New("clearing record belongs to another order")
	ErrInvalidClearingState  = errors.New("invalid clearing state")
	ErrOrderNotCompleted     = errors.New("order not completed")
)

type Order struct {
	ID     uint
	Status OrderStatus
}

type ClearingRecord struct {
	OrderID uint
	Status  ClearingStatus
}

// DecideSettlement 判断本次调用应执行结算、幂等返回，还是拒绝。
func DecideSettlement(o Order, record *ClearingRecord) (Decision, error) {
	// TODO 1：拒绝 ID 为 0 的订单；清算单缺失时返回 ErrMissingClearing。
	// TODO 2：清算单必须属于当前订单，否则返回 ErrClearingOrderMismatch。
	// TODO 3：settled/refunded 是终态，重复调用应返回 DecisionNoop。
	// TODO 4：只有 cleared 能继续；未知或空清算状态返回 ErrInvalidClearingState。
	// TODO 5：退款中/已退款的订单返回 DecisionNoop，避免结算与退款争抢资金。
	// TODO 6：只有 completed + cleared 返回 DecisionSettle；其他订单状态返回 ErrOrderNotCompleted。
	return "", nil
}
