package clearing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/RedInn7/gomall/consts"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
)

const settleQueue = "clearing.settle"

var errSettlePoisonMessage = errors.New("clearing settle: poison message")

func HandleOrderCompletedEvent(ctx context.Context, payload []byte) error {
	var evt events.OrderCompletedEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return fmt.Errorf("%w: %v", errSettlePoisonMessage, err)
	}
	if evt.OrderID == 0 {
		return fmt.Errorf("%w: missing order_id", errSettlePoisonMessage)
	}
	return SettleCompletedOrder(ctx, evt.OrderID)
}

func HandleRefundRejectedEvent(ctx context.Context, payload []byte) error {
	var evt events.OrderRefundRejected
	if err := json.Unmarshal(payload, &evt); err != nil {
		return fmt.Errorf("%w: %v", errSettlePoisonMessage, err)
	}
	if evt.OrderID == 0 {
		return fmt.Errorf("%w: missing order_id", errSettlePoisonMessage)
	}
	// WaitShip / WaitReceive 的退款被驳回后只是恢复履约，不应提前给卖家放款。
	if evt.RestoredType != consts.OrderCompleted {
		return nil
	}
	return SettleCompletedOrder(ctx, evt.OrderID)
}

func DispatchSettleEvent(ctx context.Context, routingKey string, payload []byte) error {
	switch routingKey {
	case "order.completed":
		return HandleOrderCompletedEvent(ctx, payload)
	case "order.refund_rejected":
		return HandleRefundRejectedEvent(ctx, payload)
	default:
		return fmt.Errorf("%w: unexpected routing key %q", errSettlePoisonMessage, routingKey)
	}
}

// StartSettleConsumer 以独立队列消费 order.completed / order.refund_rejected。数据库抖动重排，
// 解码错误等毒消息和超过投递上限的消息进入 DLQ；重复事件由清算状态机吸收。
func StartSettleConsumer(ctx context.Context) error {
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		return err
	}
	for _, pattern := range []string{"order.completed", "order.refund_rejected"} {
		if err := rabbitmq.BindDomainQueue(settleQueue, pattern); err != nil {
			return err
		}
	}
	rabbitmq.SuperviseDomainConsumer(settleQueue, 16, func(d amqp.Delivery) {
		err := DispatchSettleEvent(ctx, d.RoutingKey, d.Body)
		if err == nil {
			_ = d.Ack(false)
			return
		}
		util.LogrusObj.Errorf("clearing settle handle key=%s err=%v", d.RoutingKey, err)
		poison := errors.Is(err, errSettlePoisonMessage)
		if poison || rabbitmq.ExceededDeliveryLimit(d) {
			rabbitmq.RouteToDLQ(d, settleQueue, d.RoutingKey, poison)
			return
		}
		_ = d.Nack(false, true)
	})
	return nil
}
