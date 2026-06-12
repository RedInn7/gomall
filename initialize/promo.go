package initialize

import (
	"context"

	"github.com/RedInn7/gomall/internal/promo"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// InitPromoReleaseConsumer 启动满减预算退还消费者（order.cancelled / order.refunded）。
// RMQ 不可用时跳过：事件仍由 outbox 暂存，RMQ 恢复并重启后即可续投，预算不丢。
func InitPromoReleaseConsumer(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RabbitMQ 未初始化，跳过满减预算退还消费者启动")
		return
	}
	if err := promo.StartReleaseConsumer(ctx); err != nil {
		util.LogrusObj.Errorf("StartReleaseConsumer failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("Promo release consumer started")
}
