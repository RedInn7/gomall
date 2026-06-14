package initialize

import (
	"context"

	"github.com/RedInn7/gomall/internal/redpacket"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// InitRedPacketSettleConsumer 启动红包钱包结算消费者（red_packet.*）。
// 独立队列 redpacket.settle，事件驱动地兜底再结算发/抢/退资金：
// 发/抢/退资金已在各自同步事务原子落地，本消费者依赖 at-least-once + 台账幂等安全收敛重复结算。
// RMQ 不可用时跳过：事件仍由 outbox 暂存，RMQ 恢复并重启后续投，资金不丢。
func InitRedPacketSettleConsumer(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RabbitMQ 未初始化，跳过红包结算消费者启动")
		return
	}
	if err := redpacket.StartSettleConsumer(ctx); err != nil {
		util.LogrusObj.Errorf("redpacket StartSettleConsumer failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("RedPacket settle consumer started")
}
