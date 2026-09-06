package rabbitmq

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestDeliveryCountUsesPersistentRetryHeader(t *testing.T) {
	tests := []struct {
		name    string
		headers amqp.Table
		want    int64
	}{
		{name: "first delivery", want: 1},
		{name: "first retry", headers: amqp.Table{retryCountHeader: int32(1)}, want: 2},
		{name: "second retry", headers: amqp.Table{retryCountHeader: int64(2)}, want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deliveryCount(amqp.Delivery{Headers: tt.headers}); got != tt.want {
				t.Fatalf("deliveryCount()=%d want %d", got, tt.want)
			}
		})
	}
}

func TestCloneHeadersPreservesOriginalWithoutMutatingIt(t *testing.T) {
	original := amqp.Table{"trace": "abc"}
	cloned := cloneHeaders(original)
	cloned[retryCountHeader] = int64(1)
	if original[retryCountHeader] != nil || cloned["trace"] != "abc" {
		t.Fatalf("header clone leaked mutation: original=%v cloned=%v", original, cloned)
	}
}
