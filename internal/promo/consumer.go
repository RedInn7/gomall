package promo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
)

// releaseQueue 满减预算退还队列：同时绑定 order.cancelled 与 order.refunded。
// 事件 payload 自带 promo_rule_id / promo_discount_cents，消费侧不反查订单表。
const releaseQueue = "promo.release"

// errReleasePoisonMessage 标记不可重试的消息（解码失败 / 未知 routing key）。
// 消费循环据此 Nack 不重排，避免毒消息无限回灌；重发兜底交给 outbox / 死信。
var errReleasePoisonMessage = errors.New("promo release: poison message")

// HandleOrderCancelledEvent 消费 order.cancelled，把关单订单占用的满减预算退还。
// 未命中满减的订单（rule_id=0 / discount<=0）直接放行；
// 重复投递由 ReleaseDiscount 的 promo_release 台账幂等吸收。
func HandleOrderCancelledEvent(ctx context.Context, payload []byte) error {
	var evt events.OrderCancelled
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode order.cancelled payload failed: %v", err)
		return fmt.Errorf("%w: %v", errReleasePoisonMessage, err)
	}
	if evt.PromoRuleID == 0 || evt.PromoDiscountCents <= 0 {
		return nil
	}
	return GetPromoSrv().ReleaseDiscount(ctx, evt.OrderID, evt.PromoRuleID, evt.PromoDiscountCents, "cancel")
}

// HandleOrderRefundedEvent 消费 order.refunded，退款获批的订单退还满减预算。
func HandleOrderRefundedEvent(ctx context.Context, payload []byte) error {
	var evt events.OrderRefundedEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode order.refunded payload failed: %v", err)
		return fmt.Errorf("%w: %v", errReleasePoisonMessage, err)
	}
	if evt.PromoRuleID == 0 || evt.PromoDiscountCents <= 0 {
		return nil
	}
	return GetPromoSrv().ReleaseDiscount(ctx, evt.OrderID, evt.PromoRuleID, evt.PromoDiscountCents, "refund")
}

// DispatchReleaseEvent 按 routing key 分发到对应 handler。
// 独立成纯函数便于不连 RMQ 直接验证解析 / 分发逻辑。
func DispatchReleaseEvent(ctx context.Context, routingKey string, payload []byte) error {
	switch routingKey {
	case "order.cancelled":
		return HandleOrderCancelledEvent(ctx, payload)
	case "order.refunded":
		return HandleOrderRefundedEvent(ctx, payload)
	default:
		return fmt.Errorf("%w: unexpected routing key %q", errReleasePoisonMessage, routingKey)
	}
}

// StartReleaseConsumer 绑定 order.cancelled / order.refunded 并启动消费循环。
//   - 解析失败（毒消息）：直接进 DLQ 并告警，不回灌业务队列
//   - 投递次数超限：即便业务判为可重试也兜底进 DLQ，避免无限 requeue
//   - 业务处理失败（DB 抖动等）：Nack 重排，依赖 at-least-once + 台账幂等收敛
func StartReleaseConsumer(ctx context.Context) error {
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		return err
	}
	for _, pattern := range []string{"order.cancelled", "order.refunded"} {
		if err := rabbitmq.BindDomainQueue(releaseQueue, pattern); err != nil {
			return err
		}
	}
	ch, err := rabbitmq.GlobalRabbitMQ.Channel()
	if err != nil {
		return err
	}
	if err := ch.Qos(32, 0, false); err != nil {
		return err
	}
	msgs, err := ch.Consume(releaseQueue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	go func() {
		for d := range msgs {
			err := DispatchReleaseEvent(ctx, d.RoutingKey, d.Body)
			if err == nil {
				_ = d.Ack(false)
				continue
			}
			util.LogrusObj.Errorf("promo release handle key=%s err=%v", d.RoutingKey, err)
			// 毒消息（解析失败 / 未知 routing key）不可恢复，直接进 DLQ。
			// 业务可重试错误也要在投递次数超限后兜底进 DLQ，避免无限 requeue。
			poison := errors.Is(err, errReleasePoisonMessage)
			if poison || rabbitmq.ExceededDeliveryLimit(d) {
				rabbitmq.RouteToDLQ(d, releaseQueue, d.RoutingKey, poison)
				continue
			}
			_ = d.Nack(false, true)
		}
	}()
	return nil
}
