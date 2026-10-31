package outbox

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 资金事件（maxAttempts<=0）永不 dead；重试很多次后退避仍封顶 5 分钟，不会移位溢出成负数变成热重试。
// 普通事件到上限照旧 dead。
func TestMarkFailedNeverKillsFundsEvents(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:outbox-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	dao := NewOutboxDaoByDB(db)
	funds := &OutboxEvent{AggregateType: "order", EventType: "OrderCompleted", RoutingKey: "order.completed",
		Payload: "{}", Status: OutboxStatusPublishing, Attempts: 70, NextRetryAt: time.Now()}
	other := &OutboxEvent{AggregateType: "order", EventType: "OrderPaid", RoutingKey: "order.paid",
		Payload: "{}", Status: OutboxStatusPublishing, Attempts: 9, NextRetryAt: time.Now()}
	for _, ev := range []*OutboxEvent{funds, other} {
		if err := db.Create(ev).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := dao.MarkFailed(funds.ID, funds.Attempts, 0, "broker down"); err != nil {
		t.Fatal(err)
	}
	if err := dao.MarkFailed(other.ID, other.Attempts, OutboxDefaultMaxAttempts, "broker down"); err != nil {
		t.Fatal(err)
	}

	var got OutboxEvent
	if err := db.First(&got, funds.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != OutboxStatusPending || got.NextRetryAt.Before(time.Now().Add(4*time.Minute)) {
		t.Fatalf("funds event should stay pending with capped backoff: status=%d next=%v", got.Status, got.NextRetryAt)
	}
	var dead OutboxEvent
	if err := db.First(&dead, other.ID).Error; err != nil {
		t.Fatal(err)
	}
	if dead.Status != OutboxStatusDead {
		t.Fatalf("non-funds event past max attempts should be dead, got status %d", dead.Status)
	}
}
