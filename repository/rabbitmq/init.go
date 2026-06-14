package rabbitmq

import (
	"fmt"
	"strings"
	"sync/atomic"

	amqp "github.com/rabbitmq/amqp091-go"

	conf "github.com/RedInn7/gomall/config"
)

// GlobalRabbitMQ rabbitMQ链接单例
var GlobalRabbitMQ *amqp.Connection

// healthy 标记当前 broker 连接是否可用，供启动后探活/降级判断使用。
// 用 atomic.Bool 以便消费/发布侧并发读取无需加锁。
var healthy atomic.Bool

// Healthy 返回 MQ 当前是否可用。连接失败或未初始化时为 false。
func Healthy() bool {
	return healthy.Load()
}

// InitRabbitMQ 在中间件中初始化rabbitMQ链接。
// 连接失败时返回 error 由调用方决定 fail-fast 还是降级，
// 不再内部 panic，便于上层按配置区分生产/开发行为。
func InitRabbitMQ() error {
	rConfig := conf.Config.RabbitMq
	pathRabbitMQ := strings.Join([]string{rConfig.RabbitMQ, "://", rConfig.RabbitMQUser, ":", rConfig.RabbitMQPassWord, "@", rConfig.RabbitMQHost, ":", rConfig.RabbitMQPort, "/"}, "")
	conn, err := amqp.Dial(pathRabbitMQ)
	if err != nil {
		healthy.Store(false)
		return fmt.Errorf("dial rabbitmq: %w", err)
	}
	GlobalRabbitMQ = conn
	healthy.Store(true)

	// 连接被 broker 关闭（网络抖动 / 节点重启）时翻转健康标记，
	// 供探活接口与消费自愈逻辑感知断连。
	closeCh := conn.NotifyClose(make(chan *amqp.Error, 1))
	go func() {
		<-closeCh
		healthy.Store(false)
	}()
	return nil
}

// RequireOnStartup 暴露配置中的 fail-fast 开关，供启动流程决定连不上是否中止。
func RequireOnStartup() bool {
	return conf.Config.RabbitMq != nil && conf.Config.RabbitMq.RequireOnStartup
}
