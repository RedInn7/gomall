//go:build exercise

package settlementguard

import (
	"errors"
	"testing"
)

func TestDecideSettlement(t *testing.T) {
	tests := []struct {
		name    string
		order   Order
		record  *ClearingRecord
		want    Decision
		wantErr error
	}{
		{"completed cleared", Order{7, OrderCompleted}, &ClearingRecord{7, ClearingCleared}, DecisionSettle, nil},
		{"already settled", Order{7, OrderCompleted}, &ClearingRecord{7, ClearingSettled}, DecisionNoop, nil},
		{"clearing refunded", Order{7, OrderCompleted}, &ClearingRecord{7, ClearingRefunded}, DecisionNoop, nil},
		{"order refunding", Order{7, OrderRefunding}, &ClearingRecord{7, ClearingCleared}, DecisionNoop, nil},
		{"order refunded", Order{7, OrderRefunded}, &ClearingRecord{7, ClearingCleared}, DecisionNoop, nil},
		{"paid not completed", Order{7, OrderPaid}, &ClearingRecord{7, ClearingCleared}, "", ErrOrderNotCompleted},
		{"created not completed", Order{7, OrderCreated}, &ClearingRecord{7, ClearingCleared}, "", ErrOrderNotCompleted},
		{"missing clearing", Order{7, OrderCompleted}, nil, "", ErrMissingClearing},
		{"wrong order", Order{7, OrderCompleted}, &ClearingRecord{8, ClearingCleared}, "", ErrClearingOrderMismatch},
		{"unknown clearing", Order{7, OrderCompleted}, &ClearingRecord{7, "pending"}, "", ErrInvalidClearingState},
		{"zero id", Order{0, OrderCompleted}, &ClearingRecord{0, ClearingCleared}, "", ErrInvalidOrderID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecideSettlement(tt.order, tt.record)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
