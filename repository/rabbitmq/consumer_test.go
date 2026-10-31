package rabbitmq

import (
	"context"
	"io"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"

	conf "github.com/RedInn7/gomall/config"
	util "github.com/RedInn7/gomall/pkg/utils/log"
)

func TestDeliverWithRecoverRoutesPanicToConfiguredRetry(t *testing.T) {
	previousLogger := util.LogrusObj
	util.LogrusObj = logrus.New()
	t.Cleanup(func() { util.LogrusObj = previousLogger })
	delivery := amqp.Delivery{RoutingKey: "product.changed", Body: []byte(`{"product_id":1}`)}
	var recovered amqp.Delivery
	deliverWithRecover("search.product.vector-indexer", func(amqp.Delivery) {
		panic("temporary dependency failure")
	}, func(got amqp.Delivery) {
		recovered = got
	}, delivery)

	if recovered.RoutingKey != delivery.RoutingKey || string(recovered.Body) != string(delivery.Body) {
		t.Fatalf("panic delivery did not reach retry callback: %#v", recovered)
	}
}

// ctx 取消时手上那条消息照样处理完，之后不再拿新消息。
func TestConsumeFinishesInflightDeliveryAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	msgs := make(chan amqp.Delivery, 2)
	msgs <- amqp.Delivery{DeliveryTag: 1}
	msgs <- amqp.Delivery{DeliveryTag: 2}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var handled []uint64
	go func() {
		defer close(done)
		consume(ctx, consumerConfig{queue: "test", deliver: func(d amqp.Delivery) {
			handled = append(handled, d.DeliveryTag)
			if d.DeliveryTag == 1 {
				close(started)
				<-release
			}
		}}, msgs)
	}()

	<-started
	cancel()
	select {
	case <-done:
		t.Fatal("在途消息还没处理完，consume 不应返回")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-done
	if len(handled) != 1 {
		t.Fatalf("handled = %v, ctx 取消后不应再处理新消息", handled)
	}
}

// broker 连不上时消费者在退避重连；ctx 一取消就退出，不再等退避、不再重连。
func TestSuperviseConsumerStopsRetryingAfterCancel(t *testing.T) {
	prevConf, prevLogger := conf.Config, util.LogrusObj
	conf.Config = &conf.Conf{RabbitMq: &conf.RabbitMq{RabbitMQ: "amqp", RabbitMQHost: "127.0.0.1", RabbitMQPort: "1"}}
	util.LogrusObj = logrus.New()
	util.LogrusObj.Out = io.Discard
	t.Cleanup(func() { conf.Config, util.LogrusObj = prevConf, prevLogger })

	ctx, cancel := context.WithCancel(context.Background())
	superviseConsumer(ctx, consumerConfig{queue: "test", deliver: func(amqp.Delivery) {}})
	time.Sleep(50 * time.Millisecond) // 让它订阅失败、进入退避
	cancel()

	stopped := make(chan struct{})
	go func() {
		WaitConsumers()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second): // 退避间隔是 3s
		t.Fatal("ctx 取消后消费者没有退出")
	}
}
