package rabbitmq

import (
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	util "github.com/RedInn7/gomall/pkg/utils/log"
)

// consumerRetryDelay 是订阅失败 / 投递通道关闭后重新建连的退避间隔。
const consumerRetryDelay = 3 * time.Second

// consumerConfig 描述一个长生命周期消费者的订阅参数。
type consumerConfig struct {
	queue    string
	prefetch int
	// deliver 处理单条投递，自行决定 Ack/Nack。
	deliver func(amqp.Delivery)
}

// superviseConsumer 启动一个自愈消费者：在后台 goroutine 中持续订阅队列，
// 一旦 broker 关闭投递通道（连接抖动 / 节点重启 / channel 异常）就释放旧句柄，
// 退避后重新 Channel()+Qos()+Consume()，必要时重建底层连接。
// amqp091-go 不做自动恢复，这里补齐重连/重订阅以避免消费者静默死亡。
func superviseConsumer(cfg consumerConfig) {
	go func() {
		for {
			ch, msgs, err := subscribe(cfg)
			if err != nil {
				util.LogrusObj.Errorf("RabbitMQ 消费者 %s 订阅失败，%s 后重试: %v", cfg.queue, consumerRetryDelay, err)
				time.Sleep(consumerRetryDelay)
				continue
			}
			for d := range msgs {
				deliverWithRecover(cfg.queue, cfg.deliver, d)
			}
			// msgs 被 broker 关闭：旧 channel 已失效，先 Close 释放句柄避免堆积，
			// 退避后回到循环顶部重新订阅（ensureConnection 会按需重连）。
			_ = ch.Close()
			util.LogrusObj.Warnf("RabbitMQ 消费者 %s 投递通道关闭，%s 后重新订阅", cfg.queue, consumerRetryDelay)
			time.Sleep(consumerRetryDelay)
		}
	}()
}

// deliverWithRecover 包裹单条投递处理，捕获 handler panic 避免一条毒消息打挂整个消费者 goroutine。
// panic 时 Nack 重排（交由投递次数 / DLQ 兜底），不静默丢消息。
func deliverWithRecover(queue string, deliver func(amqp.Delivery), d amqp.Delivery) {
	defer func() {
		if r := recover(); r != nil {
			util.LogrusObj.Errorf("RabbitMQ 消费者 %s 处理 panic: %v", queue, r)
			_ = d.Nack(false, true)
		}
	}()
	deliver(d)
}

// SuperviseDomainConsumer 给领域包提供的自愈消费者入口：内部复用 superviseConsumer 的
// 断连重订阅 + panic 兜底，领域侧只需提供队列名、prefetch 与单条投递处理（自行 Ack/Nack/进 DLQ）。
// 调用前应先 InitDeadLetterTopology + BindDomainQueue 声明拓扑与绑定。
func SuperviseDomainConsumer(queue string, prefetch int, deliver func(amqp.Delivery)) {
	superviseConsumer(consumerConfig{queue: queue, prefetch: prefetch, deliver: deliver})
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
