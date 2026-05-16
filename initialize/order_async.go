package initialize

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service"
)

// InitOrderAsyncConsumer 声明异步下单拓扑并启动消费者
// RMQ 不可用时静默跳过：enqueue 接口会在 publish 阶段报错，旧的同步 create 不受影响
func InitOrderAsyncConsumer(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		util.LogrusObj.Warnln("RabbitMQ 未初始化，跳过异步下单消费者启动")
		return
	}
	if err := rabbitmq.InitOrderAsyncTopology(); err != nil {
		util.LogrusObj.Errorf("InitOrderAsyncTopology failed: %v", err)
		return
	}
	if err := rabbitmq.ConsumeOrderAsync(func(body []byte) error {
		return service.HandleAsyncOrderTask(ctx, body)
	}); err != nil {
		util.LogrusObj.Errorf("ConsumeOrderAsync failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("Async order consumer started")
}
