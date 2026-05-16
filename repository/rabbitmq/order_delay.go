package rabbitmq

import (
	"context"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	util "github.com/RedInn7/gomall/pkg/utils/log"
)

const (
	orderDelayExchange = "order.delay.exchange"
	orderDelayQueue    = "order.delay.queue"
	orderDeadExchange  = "order.dead.exchange"
	orderDeadQueue     = "order.dead.queue"
	orderDeadRouting   = "order.dead"
)

// OrderCancelDelay 默认延迟时长（30 分钟）
const OrderCancelDelay = 30 * time.Minute

// InitOrderDelayTopology 声明延迟队列拓扑：
//   producer → order.delay.exchange → order.delay.queue (TTL)
//   过期 → order.dead.exchange → order.dead.queue → consumer
func InitOrderDelayTopology() error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(orderDelayExchange, "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.ExchangeDeclare(orderDeadExchange, "direct", true, false, false, false, nil); err != nil {
		return err
	}

	_, err = ch.QueueDeclare(orderDelayQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange":    orderDeadExchange,
		"x-dead-letter-routing-key": orderDeadRouting,
	})
	if err != nil {
		return err
	}
	if err := ch.QueueBind(orderDelayQueue, "", orderDelayExchange, false, nil); err != nil {
		return err
	}

	if _, err := ch.QueueDeclare(orderDeadQueue, true, false, false, false, nil); err != nil {
		return err
	}
	return ch.QueueBind(orderDeadQueue, orderDeadRouting, orderDeadExchange, false, nil)
}

// PublishOrderCancelDelay 发布延迟取消任务
func PublishOrderCancelDelay(ctx context.Context, orderNum uint64, delay time.Duration) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	return ch.PublishWithContext(ctx, orderDelayExchange, "", false, false, amqp.Publishing{
		ContentType:  "text/plain",
		DeliveryMode: amqp.Persistent,
		Expiration:   strconv.FormatInt(delay.Milliseconds(), 10),
		Body:         []byte(strconv.FormatUint(orderNum, 10)),
	})
}

// ConsumeOrderCancelDelay 启动消费者，对每个超时订单调用 handler
func ConsumeOrderCancelDelay(handler func(orderNum uint64) error) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	if err := ch.Qos(16, 0, false); err != nil {
		return err
	}
	msgs, err := ch.Consume(orderDeadQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	go func() {
		for d := range msgs {
			orderNum, err := strconv.ParseUint(string(d.Body), 10, 64)
			if err != nil {
				util.LogrusObj.Errorln("delay queue body 不是合法 orderNum:", err)
				_ = d.Nack(false, false)
				continue
			}
			if err := handler(orderNum); err != nil {
				util.LogrusObj.Errorf("处理延迟关单失败 orderNum=%d err=%v\n", orderNum, err)
				_ = d.Nack(false, true) // requeue 一次，下游再决定是否进 DLX
				continue
			}
			_ = d.Ack(false)
		}
	}()
	return nil
}
