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

// ConsumeOrderCancelDelay 启动消费者，对每个超时订单调用 handler。
// 消费在自愈 supervisor 中运行：连接抖动 / channel 关闭后会自动重连并重新订阅。
func ConsumeOrderCancelDelay(handler func(orderNum uint64) error) error {
	superviseConsumer(consumerConfig{
		queue:    orderDeadQueue,
		prefetch: 16,
		deliver: func(d amqp.Delivery) {
			orderNum, err := strconv.ParseUint(string(d.Body), 10, 64)
			if err != nil {
				util.LogrusObj.Errorln("delay queue body 不是合法 orderNum:", err)
				_ = d.Nack(false, false)
				return
			}
			if err := handler(orderNum); err != nil {
				util.LogrusObj.Errorf("处理延迟关单失败 orderNum=%d err=%v\n", orderNum, err)
				// order.dead.queue 未配置 DLX，requeue 会立即回到队首重投。
				// 仅允许首次失败重投一次；再次失败（Redelivered）即转入独立死信队列，
				// 避免持续失败的毒丸消息无界回灌、占满消费者。
				if d.Redelivered {
					RouteToDLQ(d, orderDeadQueue, orderDeadRouting, false)
					return
				}
				_ = d.Nack(false, true)
				return
			}
			_ = d.Ack(false)
		},
	})
	return nil
}
