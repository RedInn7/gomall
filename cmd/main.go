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
	"github.com/RedInn7/gomall/repository/es"
	"github.com/RedInn7/gomall/repository/milvus"
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
	initialize.InitOrderAsyncConsumer(context.Background())
	tryInitES(context.Background())
	tryInitWeb3Listener(context.Background())
	tryInitMilvus(context.Background())
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

// tryInitES ES 不可用时静默跳过，搜索接口会退回 DB 路径
func tryInitES(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			util.LogrusObj.Warnf("ES 初始化失败，商品搜索退化到 DB 路径: %v", r)
		}
	}()
	es.InitEs()
	initialize.InitSearch(ctx)
}

// tryInitWeb3Listener Web3 RPC 不可用时静默跳过，链下监听不影响主链路
func tryInitWeb3Listener(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			util.LogrusObj.Warnf("Web3 listener 初始化 panic: %v", r)
		}
	}()
	initialize.InitWeb3Listener(ctx)
}

// tryInitMilvus Milvus 不可用时静默跳过，语义召回能力关闭，关键词搜索不受影响
func tryInitMilvus(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			util.LogrusObj.Warnf("Milvus 初始化失败，语义召回能力关闭: %v", r)
		}
	}()
	if err := milvus.InitMilvus(); err != nil {
		util.LogrusObj.Warnf("Milvus 客户端连接失败，语义召回能力关闭: %v", err)
		return
	}
	initialize.InitMilvusCollection(ctx)
}
