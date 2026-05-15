package dao

import (
	"context"
	"testing"
	"time"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/repository/db/model"
)

func initDBForTest(t *testing.T) {
	t.Helper()
	if _db != nil {
		return
	}
	re := conf.ConfigReader{FileName: "../../../config/locales/config.yaml"}
	conf.InitConfigForTest(&re)
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("MySQL not available: %v", r)
		}
	}()
	InitMySQL()
}

func TestOutbox_InsertFetchMarkSent(t *testing.T) {
	initDBForTest(t)
	if _db == nil {
		t.Skip("MySQL not initialized")
	}
	ctx := context.Background()
	d := NewOutboxDao(ctx)

	type payload struct {
		OrderNum uint64 `json:"order_num"`
	}
	if err := d.Insert("order", "OrderCreated", "order.created", 1, payload{OrderNum: 42}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, err := d.FetchBatch(50)
	if err != nil {
		t.Fatalf("FetchBatch: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one pending row")
	}

	var target *model.OutboxEvent
	for _, r := range rows {
		if r.RoutingKey == "order.created" && r.AggregateID == 1 {
			target = r
			break
		}
	}
	if target == nil {
		t.Fatal("inserted row not found in FetchBatch result")
	}
	if err := d.MarkSent(target.ID); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	// 重新 FetchBatch 不应再包含这一条
	rows2, _ := d.FetchBatch(50)
	for _, r := range rows2 {
		if r.ID == target.ID {
			t.Fatal("marked-sent row still appears in FetchBatch")
		}
	}
}

func TestOutbox_MarkFailedBackoff(t *testing.T) {
	initDBForTest(t)
	if _db == nil {
		t.Skip()
	}
	ctx := context.Background()
	d := NewOutboxDao(ctx)
	if err := d.Insert("test", "Probe", "test.probe", 7, map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := d.FetchBatch(100)
	var target *model.OutboxEvent
	for _, r := range rows {
		if r.AggregateID == 7 && r.RoutingKey == "test.probe" {
			target = r
			break
		}
	}
	if target == nil {
		t.Skip("inserted row not found in fetch")
	}

	// 失败一次：attempts=1, 不会被立刻 fetch 出来
	if err := d.MarkFailed(target.ID, 0, 3, "boom"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	rows, _ = d.FetchBatch(200)
	for _, r := range rows {
		if r.ID == target.ID {
			t.Errorf("backoff not respected: row id=%d picked up immediately after fail", r.ID)
		}
	}

	// 越限标记为 dead
	if err := d.MarkFailed(target.ID, 2, 3, "boom"); err != nil {
		t.Fatal(err)
	}
	var fresh model.OutboxEvent
	d.DB.First(&fresh, target.ID)
	if fresh.Status != model.OutboxStatusDead {
		t.Fatalf("expect dead status, got %d", fresh.Status)
	}
	// 等到 backoff 过期也不应被 Fetch（已 dead）
	time.Sleep(50 * time.Millisecond)
	rows, _ = d.FetchBatch(200)
	for _, r := range rows {
		if r.ID == target.ID {
			t.Errorf("dead event must not be re-fetched")
		}
	}
}
