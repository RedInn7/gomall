//go:build exercise

package settlementguard

import "errors"

type OrderStatus string
type ClearingStatus string
type Decision string

const (
	OrderCreated     OrderStatus    = "created"
	OrderPaid        OrderStatus    = "paid"
	OrderCompleted   OrderStatus    = "completed"
	OrderRefunding   OrderStatus    = "refunding"
	OrderRefunded    OrderStatus    = "refunded"
	ClearingCleared  ClearingStatus = "cleared"
	ClearingSettled  ClearingStatus = "settled"
	ClearingRefunded ClearingStatus = "refunded"
	DecisionSettle   Decision       = "settle"
	DecisionNoop     Decision       = "noop"
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

func DecideSettlement(o Order, record *ClearingRecord) (Decision, error) {
	if o.ID == 0 {
		return "", ErrInvalidOrderID
	}
	if record == nil {
		return "", ErrMissingClearing
	}
	if record.OrderID != o.ID {
		return "", ErrClearingOrderMismatch
	}
	switch record.Status {
	case ClearingSettled, ClearingRefunded:
		return DecisionNoop, nil
	case ClearingCleared:
	default:
		return "", ErrInvalidClearingState
	}
	if o.Status == OrderRefunding || o.Status == OrderRefunded {
		return DecisionNoop, nil
	}
	if o.Status != OrderCompleted {
		return "", ErrOrderNotCompleted
	}
	return DecisionSettle, nil
}
