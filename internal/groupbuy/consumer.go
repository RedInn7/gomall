package groupbuy

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

// settleQueue 拼团散团退款自愈队列，绑定 groupbuy.expired。
//
// 散团的资金退回主路径已在 ExpireGroup 的事务内同步完成；本队列是 belt-and-suspenders：
// 重放 / 漏处理的 groupbuy.expired 事件在这里幂等补退（已退过的靠台账存在性预检放行），
// 防止 cron 散团那一刻 DB 抖动导致个别成员订单状态翻了但退款没落（虽同事务，仍以消费者兜底重放）。
const settleQueue = "groupbuy.settle"

// errSettlePoisonMessage 标记不可重试的消息（解码失败 / 未知 routing key），直接进 DLQ，
// 避免毒消息无限回灌；重发兜底交给 outbox / 死信。
var errSettlePoisonMessage = errors.New("groupbuy settle: poison message")

// HandleGroupbuyExpiredEvent 消费 groupbuy.expired，幂等补退每个成员的托管款。
func HandleGroupbuyExpiredEvent(ctx context.Context, payload []byte) error {
	var evt events.GroupbuyExpired
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode groupbuy.expired payload failed: %v", err)
		return fmt.Errorf("%w: %v", errSettlePoisonMessage, err)
	}
	if evt.GroupID == 0 {
		return fmt.Errorf("%w: missing group_id", errSettlePoisonMessage)
	}
	return GetGroupbuySrv().SettleExpiredRefund(ctx, evt.GroupID)
}

// DispatchSettleEvent 按 routing key 分发。纯函数便于不连 RMQ 直接验证解析 / 分发。
func DispatchSettleEvent(ctx context.Context, routingKey string, payload []byte) error {
	switch routingKey {
	case "groupbuy.expired":
		return HandleGroupbuyExpiredEvent(ctx, payload)
	default:
		return fmt.Errorf("%w: unexpected routing key %q", errSettlePoisonMessage, routingKey)
	}
}

// StartSettleConsumer 绑定 groupbuy.expired 并启动自愈消费循环。
//   - 解析失败 / 未知 routing key（毒消息）：直接进 DLQ 并告警，不回灌业务队列
//   - 投递次数超限：即便业务判为可重试也兜底进 DLQ，避免无限 requeue
//   - 业务处理失败（DB 抖动等）：Nack 重排，依赖 at-least-once + 台账幂等收敛
//
// 消费在 SuperviseDomainConsumer 中运行：连接抖动 / channel 关闭后自动重连重订阅。
func StartSettleConsumer(ctx context.Context) error {
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		return err
	}
	if err := rabbitmq.BindDomainQueue(settleQueue, "groupbuy.expired"); err != nil {
		return err
	}
	rabbitmq.SuperviseDomainConsumer(settleQueue, 16, func(d amqp.Delivery) {
		err := DispatchSettleEvent(ctx, d.RoutingKey, d.Body)
		if err == nil {
			_ = d.Ack(false)
			return
		}
		util.LogrusObj.Errorf("groupbuy settle handle key=%s err=%v", d.RoutingKey, err)
		poison := errors.Is(err, errSettlePoisonMessage)
		if poison || rabbitmq.ExceededDeliveryLimit(d) {
			rabbitmq.RouteToDLQ(d, settleQueue, d.RoutingKey, poison)
			return
		}
		_ = d.Nack(false, true)
	})
	return nil
}
