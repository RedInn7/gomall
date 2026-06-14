package initialize

import (
	"context"

	"github.com/RedInn7/gomall/internal/refund"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// InitRefundSettleConsumer 启动退款钱包结算消费者（order.refunded）。
// 独立队列 refund.settle，与满减预算退还消费者各自消费同一事件，互不影响。
// RMQ 不可用时跳过：事件仍由 outbox 暂存，RMQ 恢复并重启后续投，退款不丢。
func InitRefundSettleConsumer(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RabbitMQ 未初始化，跳过退款结算消费者启动")
		return
	}
	if err := refund.StartSettleConsumer(ctx); err != nil {
		util.LogrusObj.Errorf("StartSettleConsumer failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("Refund settle consumer started")
}
