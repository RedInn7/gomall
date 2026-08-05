package initialize

import (
	"context"

	"github.com/RedInn7/gomall/internal/clearing"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// InitClearingSettleConsumer 启动普通订单履约完成后的卖家结算消费者。
func InitClearingSettleConsumer(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RabbitMQ 未初始化，跳过支付结算消费者启动")
		return
	}
	if err := clearing.StartSettleConsumer(ctx); err != nil {
		util.LogrusObj.Errorf("Start clearing settle consumer failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("Clearing settle consumer started")
}
