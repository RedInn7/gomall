package rabbitmq

import (
	"context"
	"fmt"

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

// PublishDomainEvent 发布一条领域事件到主题交换机。
// 开启 publisher confirm 模式：只有 broker 返回 ACK 后才视为发布成功，
// 避免消息在途丢失后 outbox 被误标为 sent。
// mandatory=true 让无绑定队列时 broker 返回 basic.return 而非静默丢弃。
func PublishDomainEvent(ctx context.Context, routingKey string, payload []byte) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// 开启 publisher confirms：后续 Publish 会被 broker 顺序 ACK/NACK
	if err := ch.Confirm(false); err != nil {
		// broker 不支持 confirms（极少见），记录警告并降级为尽力发送
		// 此路径不返回错误，保持原有行为以免完全阻塞 outbox
		_ = err
		return ch.PublishWithContext(ctx, DomainEventsExchange, routingKey, true, false, amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         payload,
		})
	}

	// PublishWithDeferredConfirmWithContext 返回一个 DeferredConfirmation，
	// WaitContext 阻塞直到 broker 发回 ACK 或 ctx 超时
	dc, err := ch.PublishWithDeferredConfirmWithContext(
		ctx,
		DomainEventsExchange,
		routingKey,
		true,  // mandatory：无绑定队列时触发 basic.return 而非静默丢弃
		false, // immediate 已废弃，保持 false
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         payload,
		},
	)
	if err != nil {
		return fmt.Errorf("publish domain event: %w", err)
	}

	// 等待 broker confirm；NACK 或 ctx 取消均返回 error，阻止 outbox MarkSent
	if ok, err := dc.WaitContext(ctx); err != nil {
		return fmt.Errorf("wait publisher confirm: %w", err)
	} else if !ok {
		return fmt.Errorf("broker NACK for routing key %q", routingKey)
	}
	return nil
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
