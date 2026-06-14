package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// web3SettleQueue 链上确认结算队列，绑定 web3.payment.confirmed。
const web3SettleQueue = "web3.settle"

// errWeb3PoisonMessage 标记不可重试的消息（解码失败 / order_id 非法 / 金额不足），直接进 DLQ。
var errWeb3PoisonMessage = errors.New("web3 settle: poison message")

// web3ConfirmedEvent 与 listener 写入 outbox 的 PaymentConfirmed JSON 结构对齐。
type web3ConfirmedEvent struct {
	OrderID string `json:"order_id"` // 0x bytes32 hex，合约内编码的 gomall 订单 id
	Buyer   string `json:"buyer"`
	Amount  string `json:"amount"` // 代币最小单位（USDC 6 位 / ETH wei），string 防精度损失
	TxHash  string `json:"tx_hash"`
}

// decodeOrderIDFromBytes32 把合约 bytes32 hex 解析回 gomall 订单 id。
// 合约把 order.ID 编码进 bytes32（低位），这里按大整数还原。
func decodeOrderIDFromBytes32(hexStr string) (uint, error) {
	s := strings.TrimSpace(hexStr)
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return 0, errors.New("空 order id")
	}
	n, ok := new(big.Int).SetString(s, 16)
	if !ok || n.Sign() <= 0 || !n.IsUint64() {
		return 0, fmt.Errorf("非法 order id hex: %q", hexStr)
	}
	return uint(n.Uint64()), nil
}

// HandleWeb3PaymentConfirmed 消费 web3.payment.confirmed：解码订单 id + 校验金额 + 结算订单。
// 解码 / order_id 非法 / 金额不足 → 毒消息进 DLQ；DB 抖动等可重试错误 → 由调用方 Nack 重排。
func HandleWeb3PaymentConfirmed(ctx context.Context, payload []byte) error {
	var ev web3ConfirmedEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		util.LogrusObj.Errorf("decode web3.payment.confirmed payload failed: %v", err)
		return fmt.Errorf("%w: %v", errWeb3PoisonMessage, err)
	}
	orderID, err := decodeOrderIDFromBytes32(ev.OrderID)
	if err != nil {
		util.LogrusObj.Errorf("web3 confirmed bad order id: %v", err)
		return fmt.Errorf("%w: %v", errWeb3PoisonMessage, err)
	}

	// 把链上 buyer 地址透传到结算层，与签名 park 阶段写入 Redis 的钱包地址强校验，
	// 杜绝“凑出匹配金额的事件即可结算任意订单”。空 buyer 视为毒消息（事件解码异常）。
	if strings.TrimSpace(ev.Buyer) == "" {
		util.LogrusObj.Errorf("web3 confirmed missing buyer order=%d", orderID)
		return fmt.Errorf("%w: missing buyer", errWeb3PoisonMessage)
	}

	if err := GetWeb3SettleSrv().SettleConfirmedOrder(ctx, orderID, ev.Buyer, ev.Amount); err != nil {
		// 金额不足 / 喂价缺失 / buyer 不匹配：不可靠重投解决，标记毒消息进 DLQ 人工核查
		//（防资损 / 配置问题 / 越权结算）。
		if errors.Is(err, ErrWeb3AmountMismatch) || errors.Is(err, ErrWeb3PriceNotConfigured) || errors.Is(err, ErrWeb3BuyerMismatch) {
			return fmt.Errorf("%w: %v", errWeb3PoisonMessage, err)
		}
		return err // 其它（DB 抖动等）可重试
	}
	return nil
}

// DispatchWeb3SettleEvent 按 routing key 分发，纯函数便于不连 RMQ 直接验证。
func DispatchWeb3SettleEvent(ctx context.Context, routingKey string, payload []byte) error {
	switch routingKey {
	case "web3.payment.confirmed":
		return HandleWeb3PaymentConfirmed(ctx, payload)
	default:
		return fmt.Errorf("%w: unexpected routing key %q", errWeb3PoisonMessage, routingKey)
	}
}

// web3SettlePrefetch 单次预取条数：链上结算走 DB 事务，控制并发避免锁竞争。
const web3SettlePrefetch = 32

// StartWeb3SettleConsumer 绑定 web3.payment.confirmed 并启动自愈消费循环（毒消息进 DLQ，可重试 Nack 重排）。
//   - 解析失败 / order_id 非法 / 金额不足 / buyer 不匹配（毒消息）：直接进 DLQ 并告警
//   - 投递次数超限：即便业务判为可重试也兜底进 DLQ，避免无限 requeue
//   - 业务可重试错误（DB 抖动等）：Nack 重排，依赖 at-least-once + 台账幂等收敛
//
// 复用 SuperviseDomainConsumer 提供断连重订阅 + panic 兜底，替换原裸 ch.Consume
// 首次断连即永久停摆的实现。
func StartWeb3SettleConsumer(ctx context.Context) error {
	if err := rabbitmq.InitDeadLetterTopology(); err != nil {
		return err
	}
	if err := rabbitmq.BindDomainQueue(web3SettleQueue, "web3.payment.confirmed"); err != nil {
		return err
	}
	rabbitmq.SuperviseDomainConsumer(web3SettleQueue, web3SettlePrefetch, func(d amqp.Delivery) {
		err := DispatchWeb3SettleEvent(ctx, d.RoutingKey, d.Body)
		if err == nil {
			_ = d.Ack(false)
			return
		}
		util.LogrusObj.Errorf("web3 settle handle key=%s err=%v", d.RoutingKey, err)
		poison := errors.Is(err, errWeb3PoisonMessage)
		if poison || rabbitmq.ExceededDeliveryLimit(d) {
			rabbitmq.RouteToDLQ(d, web3SettleQueue, d.RoutingKey, poison)
			return
		}
		_ = d.Nack(false, true)
	})
	return nil
}
