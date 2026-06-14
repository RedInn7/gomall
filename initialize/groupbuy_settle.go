package initialize

import (
	"context"

	"github.com/RedInn7/gomall/internal/groupbuy"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// InitGroupbuySettleConsumer 启动拼团散团退款自愈消费者（groupbuy.expired）。
//
// 散团的资金退回主路径在 ExpireGroup 事务内同步完成；本消费者是兜底重放路径，
// 对 groupbuy.expired 幂等补退（已退过的靠台账存在性预检放行）。
// RMQ 不可用时跳过：事件仍由 outbox 暂存，RMQ 恢复并重启后续投，退款不丢。
func InitGroupbuySettleConsumer(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RabbitMQ 未初始化，跳过拼团结算消费者启动")
		return
	}
	if err := groupbuy.StartSettleConsumer(ctx); err != nil {
		util.LogrusObj.Errorf("groupbuy StartSettleConsumer failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("Groupbuy settle consumer started")
}
