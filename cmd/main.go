package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/apache/skywalking-go"
	"github.com/robfig/cron/v3"

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

// backgroundShutdownTimeout HTTP 关停后，等 cron、MQ 消费者、outbox 等后台任务收尾的上限。
const backgroundShutdownTimeout = 20 * time.Second

func main() {
	// 根 ctx：收到 SIGINT / SIGTERM 时取消，后台组件据此停止接新活。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// 第一次信号之后恢复默认处理：启动或收尾卡住时，再发一次信号就直接结束进程。
	context.AfterFunc(ctx, stop)

	c := loading(ctx)
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

	<-ctx.Done()

	util.LogrusObj.Info("shutting down server...")
	// ctx 已取消：MQ 消费者、outbox 发布器、Xcash 对账不再接新活；cron 也停止调度新任务
	cronStopped := c.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		util.LogrusObj.Errorf("server forced shutdown: %v", err)
	}

	// 等后台把在途的任务做完再退出：cron 里在跑的任务、消费者手上的消息、outbox 已认领的那批
	var wg sync.WaitGroup
	wg.Go(func() { <-cronStopped.Done() })
	wg.Go(rabbitmq.WaitConsumers)
	wg.Go(initialize.WaitBackground)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		util.LogrusObj.Info("shutdown complete")
	case <-time.After(backgroundShutdownTimeout):
		util.LogrusObj.Warnf("后台任务 %s 内没有收尾完，直接退出", backgroundShutdownTimeout)
	}
}

// loading 加载配置、连接依赖并启动后台组件。ctx 是根 ctx，返回的 cron 由 main 在退出时停止。
func loading(ctx context.Context) *cron.Cron {
	conf.InitConfig()
	util.InitLog() // 必须在使用 LogrusObj 的初始化之前完成
	// 节点号配错要在连库、迁移之前就退出，避免先改了表结构才发现起不来
	snowflake.InitSnowflake(snowflakeNodeID())
	dao.InitMySQL()
	if err := migrate.Run(); err != nil {
		panic(err)
	}
	cache.InitCache()
	c := initialize.InitCron(ctx)
	// 大数据集的库存预热可能需要数分钟，InitInventory 在后台执行，让商品浏览、登录等
	// 不依赖库存桶的接口先开始服务；还没建桶的商品下单时按 DB 补建。
	initialize.InitInventory(ctx)
	tryInitRabbitMQ(ctx)
	initialize.InitOutboxPublisher(ctx)
	// 消费者的 handler 用这个 ctx 写库、发消息，不能随退出信号取消，否则在途消息会半路失败；
	// 消费者什么时候停止拉新消息由 InitRabbitMQ(ctx) 控制。
	handlerCtx := context.WithoutCancel(ctx)
	initialize.InitOrderAsyncConsumer(handlerCtx)
	initialize.InitPromoReleaseConsumer(handlerCtx)
	initialize.InitRefundSettleConsumer(handlerCtx)
	initialize.InitClearingSettleConsumer(handlerCtx)
	initialize.InitRedPacketSettleConsumer(handlerCtx)
	initialize.InitGroupbuySettleConsumer(handlerCtx)
	initialize.InitWeb3SettleConsumer(handlerCtx)
	initialize.InitXcashReconciler(ctx)
	// 先装配 Milvus，再启动商品索引消费者，避免启动窗口内的商品事件只写 ES。
	tryInitMilvus(ctx)
	tryInitES(ctx)
	tryInitWeb3Listener(ctx)
	fmt.Println("加载配置完成...")
	return c
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
func tryInitRabbitMQ(ctx context.Context) {
	if err := rabbitmq.InitRabbitMQ(ctx); err != nil {
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
