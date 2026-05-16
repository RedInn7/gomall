package rabbitmq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
)

// 异步下单链路：enqueue 接口写消息到 OrderAsyncExchange，
// consumer 落 DB + outbox，前端用 ticket 轮询 Redis 拿结果。
const (
	OrderAsyncExchange = "order.create.async"
	OrderAsyncQueue    = "order.create.async.queue"
	OrderAsyncRouting  = "create"
)

// InitOrderAsyncTopology 声明异步下单交换机/队列拓扑
func InitOrderAsyncTopology() error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(OrderAsyncExchange, "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(OrderAsyncQueue, true, false, false, false, nil); err != nil {
		return err
	}
	return ch.QueueBind(OrderAsyncQueue, OrderAsyncRouting, OrderAsyncExchange, false, nil)
}

// PublishOrderAsync 把一条下单任务投到异步下单交换机
func PublishOrderAsync(ctx context.Context, payload []byte) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	return ch.PublishWithContext(ctx, OrderAsyncExchange, OrderAsyncRouting, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         payload,
	})
}

// ConsumeOrderAsync 启动异步下单消费者，handler 返回 nil 时 Ack，否则 Nack 不重投
// 重试逻辑交给上层（失败要释放预扣库存 + 写 ticket 失败态，不能让 RMQ 无限 requeue）
func ConsumeOrderAsync(handler func(body []byte) error) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	if err := ch.Qos(32, 0, false); err != nil {
		return err
	}
	msgs, err := ch.Consume(OrderAsyncQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	go func() {
		for d := range msgs {
			if err := handler(d.Body); err != nil {
				_ = d.Nack(false, false)
				continue
			}
			_ = d.Ack(false)
		}
	}()
	return nil
}
