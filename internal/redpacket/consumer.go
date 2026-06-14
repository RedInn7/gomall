package redpacket

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

// settleQueue 红包钱包结算队列，独立绑定 red_packet.*。
// 发/抢/退的资金已在各自同步事务内原子落地，本消费者为事件驱动的兜底再结算：
// at-least-once 投递 + 台账唯一索引幂等，使重复结算安全收敛，绝不二次扣/二次入账。
const settleQueue = "redpacket.settle"

// errSettlePoisonMessage 标记不可重试的消息（解码失败 / 未知 routing key / 关键字段缺失）。
// 据此直接进 DLQ，避免毒消息无限回灌。
var errSettlePoisonMessage = errors.New("redpacket settle: poison message")

// HandleRedPacketCreatedEvent 发包资金兜底结算：发包人 debit / 平台清算 credit。
// 同步发包事务已落地，此处重复投递由台账幂等吸收。
func HandleRedPacketCreatedEvent(ctx context.Context, payload []byte) error {
	var evt events.RedPacketCreated
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode red_packet.created payload failed: %v", err)
		return fmt.Errorf("%w: %v", errSettlePoisonMessage, err)
	}
	if evt.RedPacketID == 0 || evt.UserID == 0 || evt.Total <= 0 {
		return fmt.Errorf("%w: invalid created event", errSettlePoisonMessage)
	}
	return GetRedPacketSrv().SettleSend(ctx, evt.RedPacketID, evt.UserID, evt.Total)
}

// HandleRedPacketClaimedEvent 领包资金兜底结算：领取人 credit / 平台清算 debit。
// 入账 ref 用领取记录 id，由 (red_packet_id, user_id) 反查（uniq 约束保证唯一一行）。
func HandleRedPacketClaimedEvent(ctx context.Context, payload []byte) error {
	var evt events.RedPacketClaimed
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode red_packet.claimed payload failed: %v", err)
		return fmt.Errorf("%w: %v", errSettlePoisonMessage, err)
	}
	if evt.RedPacketID == 0 || evt.UserID == 0 || evt.Amount <= 0 {
		return fmt.Errorf("%w: invalid claimed event", errSettlePoisonMessage)
	}
	claim, err := NewRedPacketDao(ctx).GetClaim(evt.RedPacketID, evt.UserID)
	if err != nil {
		// 领取记录尚未可见（主从延迟等）：可重试，交由 Nack 重排。
		return err
	}
	if claim == nil || claim.ID == 0 {
		// 无对应领取记录，无法定位 ref，判脏事件进 DLQ。
		return fmt.Errorf("%w: claim not found rp=%d uid=%d", errSettlePoisonMessage, evt.RedPacketID, evt.UserID)
	}
	return GetRedPacketSrv().SettleClaim(ctx, claim.ID, evt.UserID, claim.Amount)
}

// HandleRedPacketExpiredEvent 过期回收资金兜底结算：剩余金额从 escrow 退回发包人。
func HandleRedPacketExpiredEvent(ctx context.Context, payload []byte) error {
	var evt events.RedPacketExpired
	if err := json.Unmarshal(payload, &evt); err != nil {
		util.LogrusObj.Errorf("decode red_packet.expired payload failed: %v", err)
		return fmt.Errorf("%w: %v", errSettlePoisonMessage, err)
	}
	if evt.RedPacketID == 0 || evt.UserID == 0 {
		return fmt.Errorf("%w: invalid expired event", errSettlePoisonMessage)
	}
	if evt.RefundTotal <= 0 {
		return nil
	}
	return GetRedPacketSrv().SettleRefund(ctx, evt.RedPacketID, evt.UserID, evt.RefundTotal)
}

// DispatchSettleEvent 按 routing key 分发。独立成纯函数便于不连 RMQ 直接验证分发逻辑。
func DispatchSettleEvent(ctx context.Context, routingKey string, payload []byte) error {
	switch routingKey {
	case "red_packet.created":
		return HandleRedPacketCreatedEvent(ctx, payload)
	case "red_packet.claimed":
		return HandleRedPacketClaimedEvent(ctx, payload)
	case "red_packet.expired":
		return HandleRedPacketExpiredEvent(ctx, payload)
	default:
		return fmt.Errorf("%w: unexpected routing key %q", errSettlePoisonMessage, routingKey)
	}
}

// StartSettleConsumer 绑定 red_packet.* 并启动自愈消费循环。
//   - 解析失败 / 未知 routing key / 关键字段缺失（毒消息）：直接进 DLQ 告警，不回灌业务队列
//   - 投递次数超限：即便可重试也兜底进 DLQ，避免无限 requeue
//   - 业务处理失败（DB 抖动等）：Nack 重排，依赖 at-least-once + 台账幂等收敛
//
// 消费在 SuperviseDomainConsumer 中运行：连接抖动 / channel 关闭后自动重连重订阅。
func StartSettleConsumer(ctx context.Context) error {
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		return err
	}
	if err := rabbitmq.BindDomainQueue(settleQueue, "red_packet.*"); err != nil {
		return err
	}
	rabbitmq.SuperviseDomainConsumer(settleQueue, 16, func(d amqp.Delivery) {
		err := DispatchSettleEvent(ctx, d.RoutingKey, d.Body)
		if err == nil {
			_ = d.Ack(false)
			return
		}
		util.LogrusObj.Errorf("redpacket settle handle key=%s err=%v", d.RoutingKey, err)
		poison := errors.Is(err, errSettlePoisonMessage)
		if poison || rabbitmq.ExceededDeliveryLimit(d) {
			rabbitmq.RouteToDLQ(d, settleQueue, d.RoutingKey, poison)
			return
		}
		_ = d.Nack(false, true)
	})
	return nil
}
