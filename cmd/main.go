package main

import (
	"context"
	"fmt"

	_ "github.com/apache/skywalking-go"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/initialize"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	snowflake "github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/routes"
)

func main() {
	loading() // 加载配置
	r := routes.NewRouter()
	_ = r.Run(conf.Config.System.HttpPort)
	fmt.Println("启动配成功...")
}

// loading一些配置
func loading() {
	conf.InitConfig()
	util.InitLog() // 必须在使用 LogrusObj 的初始化之前完成
	dao.InitMySQL()
	cache.InitCache()
	snowflake.InitSnowflake(1)
	initialize.InitCron()
	initialize.InitInventory(context.Background())
	tryInitRabbitMQ()
	initialize.InitOutboxPublisher(context.Background())
	//es.InitEs()             // 如果需要接入ELK可以打开这个注释
	//kafka.InitKafka()
	//track.InitJaeger()
	fmt.Println("加载配置完成...")
	//go scriptStarting()
}

func scriptStarting() {
	// 启动一些脚本
}

// tryInitRabbitMQ RabbitMQ 不可用时不阻塞启动，但放弃延迟队列能力
func tryInitRabbitMQ() {
	defer func() {
		if r := recover(); r != nil {
			util.LogrusObj.Warnf("RabbitMQ 初始化失败，订单延迟关单功能不可用: %v", r)
		}
	}()
	rabbitmq.InitRabbitMQ()
	initialize.InitOrderDelayConsumer()
}
