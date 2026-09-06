package rabbitmq

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const retryCountHeader = "x-gomall-retry-count"

// InitRetryQueue 声明固定延迟重试队列。TTL 到期后，消息经默认交换机回到原队列。
func InitRetryQueue(queue string, delay time.Duration) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	_, err = ch.QueueDeclare(queue+".retry", true, false, false, false, amqp.Table{
		"x-message-ttl":             int32(delay / time.Millisecond),
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": queue,
	})
	return err
}

// RetryToQueueOrDLQ 将失败投递送入延迟队列；达到配置上限后送共享 DLQ。
func RetryToQueueOrDLQ(d amqp.Delivery, queue, routingKey string) {
	if deliveryCount(d) >= maxDeliveryAttempts() {
		RouteToDLQ(d, queue, routingKey, false)
		return
	}
	headers := cloneHeaders(d.Headers)
	headers[retryCountHeader] = retryCount(d) + 1
	pub := amqp.Publishing{Headers: headers, ContentType: d.ContentType, DeliveryMode: amqp.Persistent, Body: d.Body}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := publishConfirmed(ctx, "", queue+".retry", pub); err != nil {
		_ = d.Nack(false, true)
		return
	}
	_ = d.Ack(false)
}

func publishConfirmed(ctx context.Context, exchange, routingKey string, pub amqp.Publishing) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.Confirm(false); err != nil {
		return ch.PublishWithContext(ctx, exchange, routingKey, false, false, pub)
	}
	dc, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, false, false, pub)
	if err != nil {
		return err
	}
	if ok, err := dc.WaitContext(ctx); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("broker NACK for retry queue %s", routingKey)
	}
	return nil
}

func cloneHeaders(source amqp.Table) amqp.Table {
	out := make(amqp.Table, len(source)+1)
	for key, value := range source {
		out[key] = value
	}
	return out
}

func retryCount(d amqp.Delivery) int64 {
	switch count := d.Headers[retryCountHeader].(type) {
	case int64:
		return count
	case int32:
		return int64(count)
	case int:
		return int64(count)
	default:
		return 0
	}
}
