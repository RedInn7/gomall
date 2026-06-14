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
				cfg.deliver(d)
			}
			// msgs 被 broker 关闭：旧 channel 已失效，先 Close 释放句柄避免堆积，
			// 退避后回到循环顶部重新订阅（ensureConnection 会按需重连）。
			_ = ch.Close()
			util.LogrusObj.Warnf("RabbitMQ 消费者 %s 投递通道关闭，%s 后重新订阅", cfg.queue, consumerRetryDelay)
			time.Sleep(consumerRetryDelay)
		}
	}()
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
