package initialize

import (
	"fmt"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service"
	"github.com/robfig/cron/v3"
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
	c.Start()

}
