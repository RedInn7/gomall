package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	util "github.com/RedInn7/gomall/pkg/utils/log"
)

const (
	retryCountHeader = "x-gomall-retry-count"
	// routingKeyHeader 记录消息最初的 routing key：经延迟队列回流后，投递上的 routing key 变成了队列名。
	routingKeyHeader = "x-gomall-routing-key"
)

// SettleRetryDelays 资金 / 库存类消费者的逐级退避：6 次重试覆盖约 8.5 小时，
// broker / DB 长时间故障恢复后能自愈，仍失败才进 DLQ 人工处理。
var SettleRetryDelays = []time.Duration{
	10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour,
}

// retryLevels 记录每个队列声明了几级延迟队列，由 InitRetryQueue 写入。
var retryLevels sync.Map

// InitRetryQueue 声明延迟重试队列。TTL 到期后，消息经默认交换机回到原队列。
// 只给一个 delay 时是固定间隔；给多个时逐级退避：第 n 次重试进第 min(n, len(delays)) 级，
// 每级至少走一次，投递上限取 len(delays)+1 与配置 MaxDeliveryAttempts 的较大者。
// 同名队列已按别的延迟声明过时 broker 返回 406：沿用已有队列并告警，不让消费者因此起不来；
// 删掉该延迟队列后重启才会换成新延迟。
func InitRetryQueue(queue string, delays ...time.Duration) error {
	if len(delays) == 0 {
		return errors.New("retry queue needs at least one delay")
	}
	for i, delay := range delays {
		if err := declareRetryQueue(queue, i+1, delay); err != nil {
			return err
		}
	}
	retryLevels.Store(queue, len(delays))
	return nil
}

// declareRetryQueue 每级单独开 channel：406 会让 broker 关掉当前 channel。
func declareRetryQueue(queue string, level int, delay time.Duration) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	name := retryQueueName(queue, level)
	_, err = ch.QueueDeclare(name, true, false, false, false, amqp.Table{
		"x-message-ttl":             int32(delay / time.Millisecond),
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": queue,
	})
	var amqpErr *amqp.Error
	if errors.As(err, &amqpErr) && amqpErr.Code == amqp.PreconditionFailed {
		util.LogrusObj.Warnf("retry queue %s 已存在且参数不同，沿用旧队列: %v", name, err)
		return nil
	}
	return err
}

// retryQueueName 第一级沿用旧名 queue.retry，与只有一级延迟的老用法（搜索索引）共用同一个队列。
func retryQueueName(queue string, level int) string {
	if level <= 1 {
		return queue + ".retry"
	}
	return fmt.Sprintf("%s.retry.%d", queue, level)
}

// nextRetryQueue 返回下一次重试要进的延迟队列；ok=false 表示投递次数已到上限，应进 DLQ。
func nextRetryQueue(d amqp.Delivery, queue string) (string, bool) {
	levels := 1
	if v, ok := retryLevels.Load(queue); ok {
		levels = v.(int)
	}
	limit := max(maxDeliveryAttempts(), int64(levels)+1)
	if deliveryCount(d) >= limit {
		return "", false
	}
	return retryQueueName(queue, min(int(retryCount(d))+1, levels)), true
}

// OriginalRoutingKey 消息最初的 routing key。经延迟队列回流的消息，投递上的 routing key 是队列名，
// 按 routing key 分发的消费者必须用它。
func OriginalRoutingKey(d amqp.Delivery) string {
	if key, ok := d.Headers[routingKeyHeader].(string); ok && key != "" {
		return key
	}
	return d.RoutingKey
}

// RetryToQueueOrDLQ 将失败投递送入下一级延迟队列；达到上限后送共享 DLQ。
func RetryToQueueOrDLQ(d amqp.Delivery, queue, routingKey string) {
	retryQueue, ok := nextRetryQueue(d, queue)
	if !ok {
		RouteToDLQ(d, queue, routingKey, false)
		return
	}
	pub := amqp.Publishing{Headers: retryHeaders(d, routingKey), ContentType: d.ContentType, DeliveryMode: amqp.Persistent, Body: d.Body}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := publishConfirmed(ctx, "", retryQueue, pub); err != nil {
		_ = d.Nack(false, true)
		return
	}
	_ = d.Ack(false)
}

// retryHeaders 重试计数 +1，并在第一次重试时记下原 routing key。
func retryHeaders(d amqp.Delivery, routingKey string) amqp.Table {
	headers := cloneHeaders(d.Headers)
	headers[retryCountHeader] = retryCount(d) + 1
	if _, ok := headers[routingKeyHeader]; !ok {
		headers[routingKeyHeader] = routingKey
	}
	return headers
}

func publishConfirmed(ctx context.Context, exchange, routingKey string, pub amqp.Publishing) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.Confirm(false); err != nil {
		return fmt.Errorf("enable publisher confirms: %w", err)
	}
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))
	dc, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, true, false, pub)
	if err != nil {
		return err
	}
	if ok, err := dc.WaitContext(ctx); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("broker NACK for retry queue %s", routingKey)
	}
	select {
	case returned := <-returns:
		return fmt.Errorf("message returned for routing key %s: %d %s", routingKey, returned.ReplyCode, returned.ReplyText)
	default:
	}
	return nil
}

func cloneHeaders(source amqp.Table) amqp.Table {
	out := make(amqp.Table, len(source)+2)
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
