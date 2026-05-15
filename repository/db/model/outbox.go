package model

import (
	"time"

	"github.com/jinzhu/gorm"
)

const (
	OutboxStatusPending = 1
	OutboxStatusSent    = 2
	OutboxStatusDead    = 3

	OutboxDefaultMaxAttempts = 10
)

// OutboxEvent 与业务写在同一个事务里，由后台 publisher 异步投递。
//   aggregate_type + aggregate_id: 这条事件属于哪个聚合根
//   event_type:                    具体动作 (OrderCreated / StockReserved / ProductUpdated 等)
//   routing_key:                   投递到 domain.events 交换机时使用 (默认 aggregate_type.event_type)
//   payload:                       JSON 序列化的事件 body
//   status:                        1 pending / 2 sent / 3 dead
//   next_retry_at:                 下一次允许重试的时间
type OutboxEvent struct {
	gorm.Model
	AggregateType string    `gorm:"size:64;not null;index:idx_outbox_dispatch,priority:2"`
	AggregateID   uint      `gorm:"not null"`
	EventType     string    `gorm:"size:64;not null"`
	RoutingKey    string    `gorm:"size:128;not null"`
	Payload       string    `gorm:"type:text;not null"`
	Status        int       `gorm:"not null;default:1;index:idx_outbox_dispatch,priority:1"`
	Attempts      int       `gorm:"not null;default:0"`
	NextRetryAt   time.Time `gorm:"not null;index:idx_outbox_dispatch,priority:3"`
	LastError     string    `gorm:"size:512"`
}

func (OutboxEvent) TableName() string { return "outbox_event" }
