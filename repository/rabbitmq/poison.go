package rabbitmq

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	conf "github.com/RedInn7/gomall/config"
	util "github.com/RedInn7/gomall/pkg/utils/log"
)

// defaultMaxDeliveryAttempts 单条消息在被判定为毒丸前允许的最大投递次数。
const defaultMaxDeliveryAttempts = 3

// 毒丸消息统一汇聚到独立死信交换机，运维可从 DeadLetterQueue 排查与人工补偿。
// 用独立 DLX 而非给业务队列加 x-dead-letter-exchange 参数，避免改动既有
// 队列声明触发 broker 406 PRECONDITION_FAILED，保持对存量队列零侵入。
const (
	DeadLetterExchange = "domain.dlx"
	DeadLetterQueue    = "domain.dlq"
	deadLetterRouting  = "dead"
)

// InitDeadLetterTopology 声明毒丸消息死信拓扑，幂等可重复调用。
func InitDeadLetterTopology() error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.ExchangeDeclare(DeadLetterExchange, "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(DeadLetterQueue, true, false, false, false, nil); err != nil {
		return err
	}
	return ch.QueueBind(DeadLetterQueue, deadLetterRouting, DeadLetterExchange, false, nil)
}

// maxDeliveryAttempts 读取配置中的阈值，缺省/非法时回落到默认值。
func maxDeliveryAttempts() int64 {
	if conf.Config != nil && conf.Config.RabbitMq != nil && conf.Config.RabbitMq.MaxDeliveryAttempts > 0 {
		return int64(conf.Config.RabbitMq.MaxDeliveryAttempts)
	}
	return defaultMaxDeliveryAttempts
}

// deliveryCount 估算消息已被投递的次数。
//   - 优先读 x-death header（消息每次因 reject/nack/expire 进死信都会累加 count），
//     这是 broker 维护的、跨重启可靠的计数。
//   - 退化情况下用 amqp.Delivery.Redelivered 区分首投与重投（只能表达 1 / >=2）。
func deliveryCount(d amqp.Delivery) int64 {
	if xdeath, ok := d.Headers["x-death"]; ok {
		if entries, ok := xdeath.([]interface{}); ok {
			var total int64
			for _, e := range entries {
				if m, ok := e.(amqp.Table); ok {
					switch c := m["count"].(type) {
					case int64:
						total += c
					case int32:
						total += int64(c)
					case int:
						total += int64(c)
					}
				}
			}
			if total > 0 {
				// x-death 记录的是已死信次数，加上当前这一次投递
				return total + 1
			}
		}
	}
	if d.Redelivered {
		return 2
	}
	return 1
}

// ExceededDeliveryLimit 判断消息投递次数是否已达上限，达到则应显式投递到 DLQ。
// 作为错误分类（毒消息 error）之外的兜底：即便业务把所有错误都判为可重试，
// 也不会让同一条消息在队列里无限回灌。
func ExceededDeliveryLimit(d amqp.Delivery) bool {
	return deliveryCount(d) >= maxDeliveryAttempts()
}

// RouteToDLQ 把一条不可恢复 / 超限的消息显式投递到死信队列并打 error 级告警，
// 随后 Ack 原消息（消息已落 DLQ，不能再回灌业务队列）。
// poison 表示是否由错误分类判定，用于区分超限丢弃，便于排查。
// queue/routingKey 仅用于日志与死信 header 定位。
func RouteToDLQ(d amqp.Delivery, queue, routingKey string, poison bool) {
	reason := "delivery limit exceeded"
	if poison {
		reason = "poison message"
	}
	count := deliveryCount(d)
	util.LogrusObj.Errorf(
		"mq route to DLQ queue=%s routingKey=%s reason=%s deliveries=%d limit=%d",
		queue, routingKey, reason, count, maxDeliveryAttempts(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := publishToDLQ(ctx, d, queue, routingKey, reason, count); err != nil {
		// 投 DLQ 失败时不能 Ack 丢消息，退回 Nack 重排靠下一轮兜底，
		// 至少保证 at-least-once 不丢。
		util.LogrusObj.Errorf("mq publish to DLQ failed, requeue queue=%s err=%v", queue, err)
		_ = d.Nack(false, true)
		return
	}
	_ = d.Ack(false)
}

// publishToDLQ 将原始消息体连同诊断 header 投到死信交换机。
// 开启 publisher confirm：只有 broker 确认 DLQ 落地才视为成功，否则返回 error，
// 由 RouteToDLQ Nack 重排原消息，避免“源队列已 Ack 但 DLQ 没收到”导致消息双双丢失。
func publishToDLQ(ctx context.Context, d amqp.Delivery, queue, routingKey, reason string, count int64) error {
	ch, err := GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	contentType := d.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	pub := amqp.Publishing{
		ContentType:  contentType,
		DeliveryMode: amqp.Persistent,
		Body:         d.Body,
		Headers: amqp.Table{
			"x-origin-queue":       queue,
			"x-origin-routing-key": routingKey,
			"x-dead-reason":        reason,
			"x-delivery-count":     count,
		},
	}

	// 开启 confirm 失败（broker 不支持，罕见）才降级为尽力发送。
	if err := ch.Confirm(false); err != nil {
		return ch.PublishWithContext(ctx, DeadLetterExchange, deadLetterRouting, false, false, pub)
	}

	dc, err := ch.PublishWithDeferredConfirmWithContext(
		ctx, DeadLetterExchange, deadLetterRouting, false, false, pub,
	)
	if err != nil {
		return err
	}
	if ok, err := dc.WaitContext(ctx); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("broker NACK for DLQ publish queue=%s", queue)
	}
	return nil
}
