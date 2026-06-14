package refund

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
)

// settleQueue 退款钱包结算队列。独立绑定 order.refunded，与 promo 的 promo.release 队列互不影响：
// 同一 order.refunded 事件被两条队列各自消费——promo 退满减预算，本队列退买家钱/扣卖家/还库存。
const settleQueue = "refund.settle"

// errSettlePoisonMessage 标记不可重试的消息（解码失败 / 未知 routing key）。
// 消费循环据此直接进 DLQ，避免毒消息无限回灌；重发兜底交给 outbox / 死信。
var errSettlePoisonMessage = errors.New("refund settle: poison message")

// HandleOrderRefundedEvent 消费 order.refunded，落地真正的资金回退 + 库存回补。
// 重复投递由 SettleRefund 内的台账幂等（(order_id, credit, refund) 唯一索引 + 预检）吸收。
func HandleOrderRefundedEvent(ctx context.Context, payload []byte) error {
	var evt events.OrderRefundedEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode order.refunded payload failed: %v", err)
		return fmt.Errorf("%w: %v", errSettlePoisonMessage, err)
	}
	if evt.OrderID == 0 {
		// 缺订单号的脏事件无法定位订单，判为毒消息进 DLQ，不无限重排。
		return fmt.Errorf("%w: missing order_id", errSettlePoisonMessage)
	}
	return GetRefundSrv().SettleRefund(ctx, evt.OrderID)
}

// DispatchSettleEvent 按 routing key 分发。独立成纯函数便于不连 RMQ 直接验证解析 / 分发逻辑。
func DispatchSettleEvent(ctx context.Context, routingKey string, payload []byte) error {
	switch routingKey {
	case "order.refunded":
		return HandleOrderRefundedEvent(ctx, payload)
	default:
		return fmt.Errorf("%w: unexpected routing key %q", errSettlePoisonMessage, routingKey)
	}
}

// StartSettleConsumer 绑定 order.refunded 并启动自愈消费循环。
//   - 解析失败 / 未知 routing key（毒消息）：直接进 DLQ 并告警，不回灌业务队列
//   - 投递次数超限：即便业务判为可重试也兜底进 DLQ，避免无限 requeue
//   - 业务处理失败（DB 抖动等）：Nack 重排，依赖 at-least-once + 台账幂等收敛
//
// 消费在 SuperviseDomainConsumer 中运行：连接抖动 / channel 关闭后自动重连重订阅，
// handler panic 也由其兜底，避免首次断连后 goroutine 退出永久静默停摆。
func StartSettleConsumer(ctx context.Context) error {
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		return err
	}
	if err := rabbitmq.BindDomainQueue(settleQueue, "order.refunded"); err != nil {
		return err
	}
	rabbitmq.SuperviseDomainConsumer(settleQueue, 16, func(d amqp.Delivery) {
		err := DispatchSettleEvent(ctx, d.RoutingKey, d.Body)
		if err == nil {
			_ = d.Ack(false)
			return
		}
		util.LogrusObj.Errorf("refund settle handle key=%s err=%v", d.RoutingKey, err)
		poison := errors.Is(err, errSettlePoisonMessage)
		if poison || rabbitmq.ExceededDeliveryLimit(d) {
			rabbitmq.RouteToDLQ(d, settleQueue, d.RoutingKey, poison)
			return
		}
		_ = d.Nack(false, true)
	})
	return nil
}
