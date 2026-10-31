package outbox

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// PublisherConfig 调度参数
type PublisherConfig struct {
	PollInterval time.Duration
	BatchSize    int
	MaxAttempts  int
}

func (c PublisherConfig) withDefaults() PublisherConfig {
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = OutboxDefaultMaxAttempts
	}
	return c
}

type Publisher struct {
	cfg     PublisherConfig
	running atomic.Bool
	done    chan struct{}
}

func New(cfg PublisherConfig) *Publisher {
	return &Publisher{cfg: cfg.withDefaults(), done: make(chan struct{})}
}

// Start 启动后台轮询循环，ctx 取消后发完当前这批再退出。重复调用是 no-op
func (p *Publisher) Start(ctx context.Context) {
	if !p.running.CompareAndSwap(false, true) {
		return
	}
	go p.loop(ctx)
}

// Done 在轮询循环退出后关闭，进程退出前用它等在途的那批事件发完。
func (p *Publisher) Done() <-chan struct{} {
	return p.done
}

func (p *Publisher) loop(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 已认领（publishing）的这批不随退出信号中断：发完并标记 sent / failed 再退出，
			// 否则要等回收窗口过后才重发，发出去但没标记的还会重复投递。
			p.drainBatch(context.WithoutCancel(ctx))
		}
	}
}

// fundsRoutingKeys 资金相关事件：结算 / 退款 / 预算退还 / 链上确认都靠它们驱动，丢一条就有一笔账对不上。
var fundsRoutingKeys = map[string]bool{
	"order.completed": true, "order.refund_rejected": true, "order.refunded": true, "order.cancelled": true,
	"groupbuy.expired": true, "web3.payment.confirmed": true,
}

func isFundsRoutingKey(key string) bool {
	return fundsRoutingKeys[key] || strings.HasPrefix(key, "red_packet.")
}

// drainBatch 取一批 pending 事件，逐条投递。
//   - 没有订阅者的事件被 broker 退回：没人要收，按已发送处理，不占着重试名额挤资金事件；
//   - 资金事件不标 dead：被退回（结算队列可能还没绑定）或投递失败都按退避一直重试，过了上限打 error 告警。
func (p *Publisher) drainBatch(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		return
	}
	rows, err := NewOutboxDao(ctx).FetchBatch(p.cfg.BatchSize)
	if err != nil {
		util.LogrusObj.Errorln("outbox FetchBatch:", err)
		return
	}
	for _, ev := range rows {
		funds := isFundsRoutingKey(ev.RoutingKey)
		err := rabbitmq.PublishDomainEvent(ctx, ev.RoutingKey, []byte(ev.Payload))
		if errors.Is(err, rabbitmq.ErrUnroutable) && !funds {
			err = nil
		}
		if err != nil {
			util.LogrusObj.Errorf("publish outbox event id=%d routing=%s failed: %v", ev.ID, ev.RoutingKey, err)
			maxAttempts := p.cfg.MaxAttempts
			if funds {
				maxAttempts = 0
				if ev.Attempts+1 >= p.cfg.MaxAttempts {
					util.LogrusObj.Errorf("[ALERT] outbox 资金事件持续投递失败，继续重试 id=%d routing=%s attempts=%d",
						ev.ID, ev.RoutingKey, ev.Attempts+1)
				}
			}
			if e := NewOutboxDao(ctx).MarkFailed(ev.ID, ev.Attempts, maxAttempts, err.Error()); e != nil {
				util.LogrusObj.Errorln("outbox MarkFailed:", e)
			}
			continue
		}
		if err := NewOutboxDao(ctx).MarkSent(ev.ID); err != nil {
			util.LogrusObj.Errorln("outbox MarkSent:", err)
		}
	}
}
