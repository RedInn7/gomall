package outbox

import (
	"context"
	"sync/atomic"
	"time"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
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
		c.MaxAttempts = model.OutboxDefaultMaxAttempts
	}
	return c
}

type Publisher struct {
	cfg     PublisherConfig
	running atomic.Bool
}

func New(cfg PublisherConfig) *Publisher {
	return &Publisher{cfg: cfg.withDefaults()}
}

// Start 启动后台轮询循环。重复调用是 no-op
func (p *Publisher) Start(ctx context.Context) {
	if !p.running.CompareAndSwap(false, true) {
		return
	}
	go p.loop(ctx)
}

func (p *Publisher) loop(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.drainBatch(ctx)
		}
	}
}

// drainBatch 取一批 pending 事件，逐条投递
func (p *Publisher) drainBatch(ctx context.Context) {
	if rabbitmq.GlobalRabbitMQ == nil {
		return
	}
	rows, err := dao.NewOutboxDao(ctx).FetchBatch(p.cfg.BatchSize)
	if err != nil {
		util.LogrusObj.Errorln("outbox FetchBatch:", err)
		return
	}
	for _, ev := range rows {
		if err := rabbitmq.PublishDomainEvent(ctx, ev.RoutingKey, []byte(ev.Payload)); err != nil {
			util.LogrusObj.Errorf("publish outbox event id=%d routing=%s failed: %v", ev.ID, ev.RoutingKey, err)
			if e := dao.NewOutboxDao(ctx).MarkFailed(ev.ID, ev.Attempts, p.cfg.MaxAttempts, err.Error()); e != nil {
				util.LogrusObj.Errorln("outbox MarkFailed:", e)
			}
			continue
		}
		if err := dao.NewOutboxDao(ctx).MarkSent(ev.ID); err != nil {
			util.LogrusObj.Errorln("outbox MarkSent:", err)
		}
	}
}
