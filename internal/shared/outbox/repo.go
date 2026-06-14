package outbox

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/repository/db/dao"
)

// OutboxStatusPublishing 介于 pending 与 sent 之间的认领中状态：
// FetchBatch 在事务内把候选行从 pending 抢占为 publishing，使多实例并发轮询时
// 同一条事件只会被一个实例取走，避免重复发布。投递成功转 sent，失败由
// MarkFailed 退回 pending 并按退避排期。
const OutboxStatusPublishing = 4

// publishingReclaimAfter 认领后超过该时长仍停留在 publishing 的行视为认领实例已崩溃，
// 允许被其他实例重新认领，避免行永久卡在 publishing。窗口取得足够大以覆盖单条
// 发布 + confirm 等待，避免误抢仍在途的正常认领。
const publishingReclaimAfter = 5 * time.Minute

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

// FetchBatch 原子认领 limit 个待发布事件 (pending + 已到重试时间)，按 id 升序保证 FIFO。
// 在单个事务内 SELECT ... FOR UPDATE SKIP LOCKED 锁定候选行，再就地把状态抢占为
// publishing：多实例并发轮询时彼此跳过已锁定行，同一条事件只被一个实例取走，
// 杜绝重复发布。投递结果由 MarkSent / MarkFailed 把行移出 publishing。
func (d *OutboxDao) FetchBatch(limit int) ([]*OutboxEvent, error) {
	var rows []*OutboxEvent
	err := d.DB.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		// pending 且到点的行，外加被崩溃实例遗留、超过回收窗口的 publishing 行。
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("(status = ? AND next_retry_at <= ?) OR (status = ? AND updated_at <= ?)",
				OutboxStatusPending, now,
				OutboxStatusPublishing, now.Add(-publishingReclaimAfter)).
			Order("id ASC").
			Limit(limit).
			Find(&rows).Error; e != nil {
			return e
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]uint, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		return tx.Model(&OutboxEvent{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":     OutboxStatusPublishing,
				"updated_at": time.Now(),
			}).Error
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (d *OutboxDao) MarkSent(id uint) error {
	return d.DB.Model(&OutboxEvent{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":     OutboxStatusSent,
			"updated_at": time.Now(),
		}).Error
}

// MarkFailed 在 SQL 内自增 attempts（gorm.Expr 避免 Go 侧读改写丢增量），
// 超过 maxAttempts 标记为 dead；否则按指数退避排期并退回 pending，
// 让该行重新可被下一轮 FetchBatch 认领（从 publishing 释放）。
func (d *OutboxDao) MarkFailed(id uint, attempts, maxAttempts int, errMsg string) error {
	now := time.Now()
	updates := map[string]any{
		"attempts":   gorm.Expr("attempts + 1"),
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
		updates["status"] = OutboxStatusPending
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
