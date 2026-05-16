package initialize

import (
	"fmt"

	"github.com/robfig/cron/v3"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service"
)

func InitCron() {
	c := cron.New(cron.WithSeconds())
	orderService := new(service.OrderTaskService)
	_, err := c.AddFunc("* */5 * * * *", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Error("Cron 任务发生 Panic: %v\n", r)
			}
		}()
		orderService.RunOrderTimeoutCheck()
	})
	if err != nil {
		panic(fmt.Sprintf("Cron 初始化失败: %v", err))
	}

	redPacketService := new(service.RedPacketTaskService)
	_, err = c.AddFunc("0 */5 * * * *", func() {
		defer func() {
			if r := recover(); r != nil {
				util.LogrusObj.Errorf("RedPacket Cron Panic: %v", r)
			}
		}()
		redPacketService.RunRedPacketExpireCheck()
	})
	if err != nil {
		panic(fmt.Sprintf("RedPacket Cron 初始化失败: %v", err))
	}

	c.Start()
}

// InitOrderDelayConsumer 声明延迟队列拓扑并启动消费者
func InitOrderDelayConsumer() {
	if err := rabbitmq.InitOrderDelayTopology(); err != nil {
		util.LogrusObj.Errorf("InitOrderDelayTopology failed: %v", err)
		return
	}
	if err := rabbitmq.ConsumeOrderCancelDelay(service.CancelUnpaidOrder); err != nil {
		util.LogrusObj.Errorf("ConsumeOrderCancelDelay failed: %v", err)
	}
}
