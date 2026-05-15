package rabbitmq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
)

// 所有业务事件统一进 domain.events 主题交换机
//   routing key 形如 "order.created" / "stock.reserved" / "product.updated"
//   消费端 bind 通配符即可订阅自己感兴趣的事件
const DomainEventsExchange = "domain.events"

// InitDomainEventsExchange 声明拓扑。Init 在 publisher / consumer 启动前都要调一次
func InitDomainEventsExchange() error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	return ch.ExchangeDeclare(DomainEventsExchange, "topic", true, false, false, false, nil)
}

// PublishDomainEvent 发布一条领域事件到主题交换机
func PublishDomainEvent(ctx context.Context, routingKey string, payload []byte) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	return ch.PublishWithContext(ctx, DomainEventsExchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         payload,
	})
}

// BindDomainQueue 给消费方创建队列并绑定到 domain.events 的某个 routing pattern
func BindDomainQueue(queue, pattern string) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return err
	}
	return ch.QueueBind(queue, pattern, DomainEventsExchange, false, nil)
}
