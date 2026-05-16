package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/types"
)

func init() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

// setupTestRedisForService 复用 cache 包的 Redis-DB-15 约定，Redis 不可用整组 skip
func setupTestRedisForService(t *testing.T) func() {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 15})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skip("Redis 127.0.0.1:6379 不可用，跳过：", err)
	}
	prevClient := cache.RedisClient
	cache.RedisClient = c
	return func() {
		c.FlushDB(context.Background())
		c.Close()
		cache.RedisClient = prevClient
	}
}

// memoryProducer 把 Publish 的 payload 攒到 channel 里，模拟 MQ
type memoryProducer struct {
	failPublish bool
	msgs        chan []byte
}

func newMemoryProducer(buffer int) *memoryProducer {
	return &memoryProducer{msgs: make(chan []byte, buffer)}
}

func (m *memoryProducer) Publish(_ context.Context, payload []byte) error {
	if m.failPublish {
		return errors.New("publish boom")
	}
	m.msgs <- payload
	return nil
}

// memoryTicketStore 内存版 ticket store
type memoryTicketStore struct {
	mu  sync.Mutex
	kv  map[string]OrderTicketStatus
	ttl map[string]time.Time
}

func newMemoryTicketStore() *memoryTicketStore {
	return &memoryTicketStore{kv: map[string]OrderTicketStatus{}, ttl: map[string]time.Time{}}
}

func (s *memoryTicketStore) Put(_ context.Context, ticket string, st OrderTicketStatus, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kv[ticket] = st
	s.ttl[ticket] = time.Now().Add(ttl)
	return nil
}

func (s *memoryTicketStore) Get(_ context.Context, ticket string) (OrderTicketStatus, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.kv[ticket]
	return st, ok, nil
}

// installAsyncTestDeps 把 producer/store/writer 都替换成可控版本，返回还原函数
func installAsyncTestDeps(t *testing.T) (*memoryProducer, *memoryTicketStore, func()) {
	t.Helper()
	prevP := defaultAsyncProducer
	prevS := defaultTicketStore
	prevW := defaultAsyncOrderWriter

	p := newMemoryProducer(16)
	s := newMemoryTicketStore()
	SetAsyncOrderProducer(p)
	SetAsyncOrderTicketStore(s)

	return p, s, func() {
		defaultAsyncProducer = prevP
		defaultTicketStore = prevS
		defaultAsyncOrderWriter = prevW
	}
}

func ensureSnowflake() {
	defer func() { _ = recover() }()
	snowflake.InitSnowflake(7)
}

func TestAsyncOrder_EnqueueReservesAndPublishes(t *testing.T) {
	cleanup := setupTestRedisForService(t)
	defer cleanup()
	ensureSnowflake()

	const pid = 7001
	if err := cache.InitStock(context.Background(), pid, 5); err != nil {
		t.Fatalf("init stock: %v", err)
	}

	prod, store, restore := installAsyncTestDeps(t)
	defer restore()

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})

	resp, err := GetOrderSrv().OrderEnqueue(ctx, &types.OrderCreateReq{
		ProductID: pid,
		Num:       2,
		Money:     1000,
		AddressID: 1,
		BossID:    9,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	r, ok := resp.(OrderEnqueueResp)
	if !ok {
		t.Fatalf("resp type %T", resp)
	}
	if r.Status != OrderTicketStatusPending || r.Ticket == "" {
		t.Fatalf("unexpected resp: %+v", r)
	}

	avail, reserved, err := cache.GetStockSnapshot(context.Background(), pid)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if avail != 3 || reserved != 2 {
		t.Fatalf("expect avail=3 reserved=2, got avail=%d reserved=%d", avail, reserved)
	}

	select {
	case body := <-prod.msgs:
		var task AsyncOrderTask
		if err := json.Unmarshal(body, &task); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if task.Ticket != r.Ticket || task.UserID != 42 || task.ProductID != pid || task.Num != 2 {
			t.Fatalf("task mismatch: %+v", task)
		}
	case <-time.After(time.Second):
		t.Fatal("no message published")
	}

	st, ok, err := store.Get(context.Background(), r.Ticket)
	if err != nil || !ok {
		t.Fatalf("ticket missing err=%v ok=%v", err, ok)
	}
	if st.Status != OrderTicketStatusPending {
		t.Fatalf("ticket status %s, want pending", st.Status)
	}
}

func TestAsyncOrder_EnqueuePublishFailReleasesReserve(t *testing.T) {
	cleanup := setupTestRedisForService(t)
	defer cleanup()
	ensureSnowflake()

	const pid = 7002
	if err := cache.InitStock(context.Background(), pid, 10); err != nil {
		t.Fatalf("init stock: %v", err)
	}

	prod, store, restore := installAsyncTestDeps(t)
	defer restore()
	prod.failPublish = true

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})

	_, err := GetOrderSrv().OrderEnqueue(ctx, &types.OrderCreateReq{
		ProductID: pid,
		Num:       3,
		AddressID: 1,
		BossID:    9,
	})
	if err == nil {
		t.Fatal("expected publish error")
	}

	avail, reserved, _ := cache.GetStockSnapshot(context.Background(), pid)
	if avail != 10 || reserved != 0 {
		t.Fatalf("expect reserve released, got avail=%d reserved=%d", avail, reserved)
	}

	// 至少应该有一个 failed ticket 被写回
	failedFound := false
	for _, st := range store.kv {
		if st.Status == OrderTicketStatusFailed {
			failedFound = true
			break
		}
	}
	if !failedFound {
		t.Fatal("expected a failed ticket in store")
	}
}

func TestAsyncOrder_ConsumerSuccessWritesTicketOK(t *testing.T) {
	cleanup := setupTestRedisForService(t)
	defer cleanup()
	ensureSnowflake()

	prod, store, restore := installAsyncTestDeps(t)
	defer restore()
	_ = prod

	// 内存版 writer：直接给 order 设一个 ID，不碰 DB
	var captured *model.Order
	SetAsyncOrderWriter(func(_ context.Context, _ AsyncOrderTask, order *model.Order) error {
		order.ID = 4242
		captured = order
		return nil
	})

	task := AsyncOrderTask{
		Ticket:    "tkt-ok-1",
		UserID:    1,
		ProductID: 1,
		Num:       1,
	}
	body, _ := json.Marshal(task)
	if err := HandleAsyncOrderTask(context.Background(), body); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if captured == nil || captured.OrderNum == 0 {
		t.Fatal("order not constructed")
	}

	st, ok, _ := store.Get(context.Background(), task.Ticket)
	if !ok || st.Status != OrderTicketStatusOK {
		t.Fatalf("ticket status: %+v ok=%v", st, ok)
	}
	if st.OrderID != captured.ID || st.OrderNum != captured.OrderNum {
		t.Fatalf("ticket ids mismatch: %+v vs order=%+v", st, captured)
	}
}

func TestAsyncOrder_ConsumerFailureReleasesReserveAndMarksFailed(t *testing.T) {
	cleanup := setupTestRedisForService(t)
	defer cleanup()
	ensureSnowflake()

	const pid = 7003
	if err := cache.InitStock(context.Background(), pid, 8); err != nil {
		t.Fatalf("init stock: %v", err)
	}
	// 先模拟 enqueue 阶段已经预扣
	if err := cache.ReserveStock(context.Background(), pid, 2); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	_, store, restore := installAsyncTestDeps(t)
	defer restore()

	SetAsyncOrderWriter(func(_ context.Context, _ AsyncOrderTask, _ *model.Order) error {
		return errors.New("db boom")
	})

	task := AsyncOrderTask{
		Ticket:    "tkt-fail-1",
		UserID:    1,
		ProductID: pid,
		Num:       2,
	}
	body, _ := json.Marshal(task)
	if err := HandleAsyncOrderTask(context.Background(), body); err == nil {
		t.Fatal("expected error from handler")
	}

	avail, reserved, _ := cache.GetStockSnapshot(context.Background(), pid)
	if avail != 8 || reserved != 0 {
		t.Fatalf("expect reserve released, got avail=%d reserved=%d", avail, reserved)
	}
	st, ok, _ := store.Get(context.Background(), task.Ticket)
	if !ok || st.Status != OrderTicketStatusFailed {
		t.Fatalf("ticket status: %+v ok=%v", st, ok)
	}
}

func TestAsyncOrder_StatusReadsBackTicket(t *testing.T) {
	cleanup := setupTestRedisForService(t)
	defer cleanup()
	ensureSnowflake()

	_, store, restore := installAsyncTestDeps(t)
	defer restore()

	const ticket = "tkt-status-1"
	if err := store.Put(context.Background(), ticket, OrderTicketStatus{
		Status:   OrderTicketStatusOK,
		OrderID:  101,
		OrderNum: 202,
	}, OrderTicketTTL); err != nil {
		t.Fatalf("put: %v", err)
	}

	resp, err := GetOrderSrv().OrderStatus(context.Background(), ticket)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	st, ok := resp.(OrderTicketStatus)
	if !ok || st.Status != OrderTicketStatusOK || st.OrderID != 101 || st.OrderNum != 202 {
		t.Fatalf("unexpected: %+v ok=%v", resp, ok)
	}

	if _, err := GetOrderSrv().OrderStatus(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing ticket")
	}
}

// 辅助：避免 strconv unused 在某些重构里被误删
var _ = strconv.Itoa
