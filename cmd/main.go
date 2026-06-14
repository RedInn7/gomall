package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/apache/skywalking-go"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/initialize"
	"github.com/RedInn7/gomall/internal/migrate"
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
	loading()
	r := routes.NewRouter()

	srv := &http.Server{
		Addr:              conf.Config.System.HttpPort,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			util.LogrusObj.Fatalf("server error: %v", err)
		}
	}()

	util.LogrusObj.Infof("server listening on %s", conf.Config.System.HttpPort)
	fmt.Println("启动配成功...")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	util.LogrusObj.Info("shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		util.LogrusObj.Errorf("server forced shutdown: %v", err)
	}
	util.LogrusObj.Info("shutdown complete")
}

// loading一些配置
func loading() {
	conf.InitConfig()
	util.InitLog() // 必须在使用 LogrusObj 的初始化之前完成
	dao.InitMySQL()
	if err := migrate.Run(); err != nil {
		panic(err)
	}
	cache.InitCache()
	snowflake.InitSnowflake(snowflakeNodeID())
	initialize.InitCron()
	initialize.InitInventory(context.Background())
	tryInitRabbitMQ()
	initialize.InitOutboxPublisher(context.Background())
	initialize.InitOrderAsyncConsumer(context.Background())
	initialize.InitPromoReleaseConsumer(context.Background())
	initialize.InitRefundSettleConsumer(context.Background())
	initialize.InitRedPacketSettleConsumer(context.Background())
	initialize.InitGroupbuySettleConsumer(context.Background())
	initialize.InitWeb3SettleConsumer(context.Background())
	tryInitES(context.Background())
	tryInitWeb3Listener(context.Background())
	tryInitMilvus(context.Background())
	fmt.Println("加载配置完成...")
}

// snowflakeNodeID 从环境变量 SNOWFLAKE_NODE_ID（或 NODE_ID）读取雪花算法节点 ID。
// 多实例部署时每个副本必须配置唯一值（0..1023），否则启动被拒绝以防止 ID 碰撞。
// 仅在设置 SNOWFLAKE_ALLOW_DEFAULT=true 时允许省略，退回节点 0（限本地开发/测试）。
func snowflakeNodeID() int64 {
	for _, envKey := range []string{"SNOWFLAKE_NODE_ID", "NODE_ID"} {
		if raw := strings.TrimSpace(os.Getenv(envKey)); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				util.LogrusObj.Fatalf("snowflakeNodeID: %s=%q is not a valid integer: %v", envKey, raw, err)
			}
			if n < 0 || n > 1023 {
				util.LogrusObj.Fatalf("snowflakeNodeID: %s=%d is out of range [0, 1023]", envKey, n)
			}
			util.LogrusObj.Infof("snowflakeNodeID: using node id %d from env %s", n, envKey)
			return n
		}
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("SNOWFLAKE_ALLOW_DEFAULT")), "true") {
		util.LogrusObj.Warn("snowflakeNodeID: SNOWFLAKE_NODE_ID / NODE_ID not set; SNOWFLAKE_ALLOW_DEFAULT=true — defaulting to node 0 (local dev/test only, NOT safe for multi-replica)")
		return 0
	}

	util.LogrusObj.Fatal("snowflakeNodeID: SNOWFLAKE_NODE_ID (or NODE_ID) must be set for each replica to prevent ID collisions; set SNOWFLAKE_ALLOW_DEFAULT=true to override for local dev")
	return 0 // unreachable, satisfies compiler
}

// tryInitRabbitMQ 初始化 RabbitMQ 连接与延迟队列消费者。
//   - 配置 requireOnStartup=true（生产）时连不上直接 panic 中止启动，避免静默降级；
//   - 否则打 error 级日志并标记不健康，订单延迟关单 / 事件消费能力关闭，主链路继续。
func tryInitRabbitMQ() {
	if err := rabbitmq.InitRabbitMQ(); err != nil {
		if rabbitmq.RequireOnStartup() {
			util.LogrusObj.Errorf("RabbitMQ 初始化失败且 requireOnStartup=true，启动中止: %v", err)
			panic(err)
		}
		util.LogrusObj.Errorf("RabbitMQ 初始化失败，订单延迟关单 / 事件消费能力关闭: %v", err)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			if rabbitmq.RequireOnStartup() {
				panic(r)
			}
			util.LogrusObj.Errorf("RabbitMQ 延迟队列消费者初始化失败: %v", r)
		}
	}()
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
