package initialize

import (
	"context"
	"time"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/outbox"
)

var globalPublisher *outbox.Publisher

// InitOutboxPublisher RMQ 不可用时不启动；用户后续修复 RMQ 后重启即可
func InitOutboxPublisher(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RabbitMQ 未初始化，跳过 Outbox publisher 启动")
		return
	}
	if err := rabbitmq.InitDomainEventsExchange(); err != nil {
		util.LogrusObj.Errorf("InitDomainEventsExchange failed: %v", err)
		return
	}
	globalPublisher = outbox.New(outbox.PublisherConfig{
		PollInterval: time.Second,
		BatchSize:    100,
	})
	globalPublisher.Start(ctx)
	util.LogrusObj.Infoln("Outbox publisher started")
}
