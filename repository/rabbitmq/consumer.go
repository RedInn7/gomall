package rabbitmq

import (
	"context"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	util "github.com/RedInn7/gomall/pkg/utils/log"
)

// consumerRetryDelay 是订阅失败 / 投递通道关闭后重新建连的退避间隔。
const consumerRetryDelay = 3 * time.Second

var (
	// consumerCtx 是所有自愈消费者的生命周期，由 InitRabbitMQ 设置：取消后消费者处理完手上的
	// 那条消息就退出，不再拉新消息、不再重连。领域包起消费者的入口不带 ctx，所以放在包级。
	consumerCtx = context.Background()
	// consumers 记录在跑的消费者 goroutine，供进程退出时 WaitConsumers 等待。
	consumers sync.WaitGroup
)

// WaitConsumers 等所有自愈消费者退出。先取消传给 InitRabbitMQ 的 ctx 再调用。
func WaitConsumers() {
	consumers.Wait()
}

// consumerConfig 描述一个长生命周期消费者的订阅参数。
type consumerConfig struct {
	queue    string
	prefetch int
	// deliver 处理单条投递，自行决定 Ack/Nack。
	deliver func(amqp.Delivery)
	onPanic func(amqp.Delivery)
}

// superviseConsumer 启动一个自愈消费者：在后台 goroutine 中持续订阅队列，
// 一旦 broker 关闭投递通道（连接抖动 / 节点重启 / channel 异常）就释放旧句柄，
// 退避后重新 Channel()+Qos()+Consume()，必要时重建底层连接。
// amqp091-go 不做自动恢复，这里补齐重连/重订阅以避免消费者静默死亡。
// ctx 取消后不再重连：处理完手上的消息、关掉 channel 就退出。
func superviseConsumer(ctx context.Context, cfg consumerConfig) {
	consumers.Add(1)
	go func() {
		defer consumers.Done()
		for ctx.Err() == nil {
			if ch, msgs, err := subscribe(cfg); err != nil {
				util.LogrusObj.Errorf("RabbitMQ 消费者 %s 订阅失败，%s 后重试: %v", cfg.queue, consumerRetryDelay, err)
			} else {
				consume(ctx, cfg, msgs)
				// msgs 被 broker 关闭，或 ctx 取消：先 Close 释放句柄避免堆积，预取了但没处理的
				// 消息由 broker 退回队列；没退出就退避后回到循环顶部重新订阅（ensureConnection 会按需重连）。
				_ = ch.Close()
				if ctx.Err() != nil {
					return
				}
				util.LogrusObj.Warnf("RabbitMQ 消费者 %s 投递通道关闭，%s 后重新订阅", cfg.queue, consumerRetryDelay)
			}
			select {
			case <-ctx.Done():
			case <-time.After(consumerRetryDelay):
			}
		}
	}()
}

// consume 逐条处理投递，直到 broker 关闭投递通道或 ctx 取消。
// ctx 只在两条消息之间检查：正在处理的那条一定跑完才返回。
func consume(ctx context.Context, cfg consumerConfig, msgs <-chan amqp.Delivery) {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case d, ok := <-msgs:
			if !ok {
				return
			}
			deliverWithRecover(cfg.queue, cfg.deliver, cfg.onPanic, d)
		}
	}
}

// deliverWithRecover 包裹单条投递处理，捕获 handler panic 避免一条毒消息打挂整个消费者 goroutine。
// panic 时 Nack 重排（交由投递次数 / DLQ 兜底），不静默丢消息。
func deliverWithRecover(queue string, deliver func(amqp.Delivery), onPanic func(amqp.Delivery), d amqp.Delivery) {
	defer func() {
		if r := recover(); r != nil {
			util.LogrusObj.Errorf("RabbitMQ 消费者 %s 处理 panic: %v", queue, r)
			if onPanic != nil {
				onPanic(d)
				return
			}
			_ = d.Nack(false, true)
		}
	}()
	deliver(d)
}

// SuperviseDomainConsumer 给领域包提供的自愈消费者入口：内部复用 superviseConsumer 的
// 断连重订阅 + panic 兜底，领域侧只需提供队列名、prefetch 与单条投递处理（自行 Ack/Nack/进 DLQ）。
// 调用前应先 InitDeadLetterTopology + BindDomainQueue 声明拓扑与绑定。
func SuperviseDomainConsumer(queue string, prefetch int, deliver func(amqp.Delivery)) {
	superviseConsumer(consumerCtx, consumerConfig{queue: queue, prefetch: prefetch, deliver: deliver})
}

// SuperviseDomainConsumerWithRetry 让 panic 也进入与普通错误相同的有界延迟重试。
// 调用方必须先 InitRetryQueue。
func SuperviseDomainConsumerWithRetry(queue string, prefetch int, deliver func(amqp.Delivery)) {
	superviseConsumer(consumerCtx, consumerConfig{
		queue: queue, prefetch: prefetch, deliver: deliver,
		onPanic: func(d amqp.Delivery) { RetryToQueueOrDLQ(d, queue, d.RoutingKey) },
	})
}

// subscribe 在一条可用连接上开 channel、设置 Qos 并发起 Consume。
// 任一步失败都会关闭已开的 channel 并返回 error 交由调用方退避重试。
func subscribe(cfg consumerConfig) (*amqp.Channel, <-chan amqp.Delivery, error) {
	conn, err := ensureConnection()
	if err != nil {
		return nil, nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, nil, err
	}
	if err := ch.Qos(cfg.prefetch, 0, false); err != nil {
		_ = ch.Close()
		return nil, nil, err
	}
	msgs, err := ch.Consume(cfg.queue, "", false, false, false, false, nil)
	if err != nil {
		_ = ch.Close()
		return nil, nil, err
	}
	return ch, msgs, nil
}
