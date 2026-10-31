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

// 资金类消费者逐级退避：每一级都走一次，走完才进 DLQ；从延迟队列回流后仍能拿到原 routing key。
func TestSettleRetryBacksOffThroughEveryLevelThenDLQ(t *testing.T) {
	const queue = "clearing.settle"
	retryLevels.Store(queue, len(SettleRetryDelays))
	t.Cleanup(func() { retryLevels.Delete(queue) })

	d := amqp.Delivery{RoutingKey: "order.completed"}
	for level := 1; level <= len(SettleRetryDelays); level++ {
		next, ok := nextRetryQueue(d, queue)
		if !ok || next != retryQueueName(queue, level) {
			t.Fatalf("retry %d: got %q ok=%v", level, next, ok)
		}
		// TTL 到期经默认交换机回流：投递上的 routing key 变成队列名。
		d = amqp.Delivery{RoutingKey: queue, Headers: retryHeaders(d, OriginalRoutingKey(d))}
		if got := OriginalRoutingKey(d); got != "order.completed" {
			t.Fatalf("retry %d lost original routing key: %q", level, got)
		}
	}
	if next, ok := nextRetryQueue(d, queue); ok {
		t.Fatalf("after %d retries the message should go to DLQ, got %q", len(SettleRetryDelays), next)
	}
}

// 只声明一级延迟队列的老用法（搜索索引）保持原样：固定队列名，按配置上限（默认 3 次投递）进 DLQ。
func TestSingleDelayRetryKeepsConfiguredLimit(t *testing.T) {
	const queue = "search.product.indexer"
	retryLevels.Store(queue, 1)
	t.Cleanup(func() { retryLevels.Delete(queue) })

	d := amqp.Delivery{RoutingKey: "product.changed"}
	for i := 0; i < 2; i++ {
		if next, ok := nextRetryQueue(d, queue); !ok || next != queue+".retry" {
			t.Fatalf("retry %d: got %q ok=%v", i+1, next, ok)
		}
		d.Headers = retryHeaders(d, d.RoutingKey)
	}
	if _, ok := nextRetryQueue(d, queue); ok {
		t.Fatal("third delivery should go to DLQ")
	}
}
