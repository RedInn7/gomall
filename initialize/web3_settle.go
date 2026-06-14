package initialize

import (
	"context"

	"github.com/RedInn7/gomall/internal/payment"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// InitWeb3SettleConsumer 启动链上确认结算消费者（web3.payment.confirmed）。
// RMQ 不可用时跳过：listener 写的确认事件仍由 outbox 暂存，RMQ 恢复重启后续投，订单不漏结算。
func InitWeb3SettleConsumer(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RabbitMQ 未初始化，跳过链上确认结算消费者启动")
		return
	}
	if err := payment.StartWeb3SettleConsumer(ctx); err != nil {
		util.LogrusObj.Errorf("StartWeb3SettleConsumer failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("Web3 settle consumer started")
}
