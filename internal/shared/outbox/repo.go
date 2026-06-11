package outbox

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

type OutboxDao struct {
	*gorm.DB
}

func NewOutboxDao(ctx context.Context) *OutboxDao {
	return &OutboxDao{dao.NewDBClient(ctx)}
}

func NewOutboxDaoByDB(db *gorm.DB) *OutboxDao {
	return &OutboxDao{db}
}

// Insert 在调用方提供的 tx 上插入事件，保证与业务写同一原子提交
func (d *OutboxDao) Insert(aggregateType, eventType, routingKey string, aggregateID uint, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	e := &OutboxEvent{
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventType:     eventType,
		RoutingKey:    routingKey,
		Payload:       string(body),
		Status:        OutboxStatusPending,
		NextRetryAt:   time.Now(),
	}
	return d.DB.Create(e).Error
}

// FetchBatch 取出 limit 个待发布事件 (pending + 已到重试时间)，按 id 升序保证 FIFO
func (d *OutboxDao) FetchBatch(limit int) ([]*OutboxEvent, error) {
	var rows []*OutboxEvent
	err := d.DB.Where("status = ? AND next_retry_at <= ?", OutboxStatusPending, time.Now()).
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (d *OutboxDao) MarkSent(id uint) error {
	return d.DB.Model(&OutboxEvent{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":     OutboxStatusSent,
			"updated_at": time.Now(),
		}).Error
}

// MarkFailed 增加 attempts，超过 maxAttempts 标记为 dead；否则按指数退避设置 next_retry_at
func (d *OutboxDao) MarkFailed(id uint, attempts, maxAttempts int, errMsg string) error {
	now := time.Now()
	updates := map[string]any{
		"attempts":   attempts + 1,
		"last_error": truncate(errMsg, 500),
		"updated_at": now,
	}
	if attempts+1 >= maxAttempts {
		updates["status"] = OutboxStatusDead
	} else {
		backoff := time.Duration(1<<attempts) * time.Second
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		updates["next_retry_at"] = now.Add(backoff)
	}
	return d.DB.Model(&OutboxEvent{}).Where("id = ?", id).Updates(updates).Error
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
