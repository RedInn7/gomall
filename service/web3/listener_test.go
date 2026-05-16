package web3

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/pkg/utils/log"
)

// --- 替身实现 ---

type fakeSubscription struct {
	errCh   chan error
	stopped chan struct{}
}

func newFakeSubscription() *fakeSubscription {
	return &fakeSubscription{errCh: make(chan error, 1), stopped: make(chan struct{})}
}
func (s *fakeSubscription) Unsubscribe() {
	select {
	case <-s.stopped:
	default:
		close(s.stopped)
	}
}
func (s *fakeSubscription) Err() <-chan error { return s.errCh }

type fakeClient struct {
	head         uint64
	filterLogs   []types.Log
	subscribeCh  chan<- types.Log
	subscription *fakeSubscription
	subOnce      sync.Once
	closed       chan struct{}
}

func newFakeClient(head uint64, filterLogs []types.Log) *fakeClient {
	return &fakeClient{head: head, filterLogs: filterLogs, closed: make(chan struct{})}
}

func (c *fakeClient) BlockNumber(ctx context.Context) (uint64, error) { return c.head, nil }
func (c *fakeClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	return c.filterLogs, nil
}
func (c *fakeClient) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	c.subOnce.Do(func() {
		c.subscribeCh = ch
		c.subscription = newFakeSubscription()
	})
	return c.subscription, nil
}
func (c *fakeClient) Close() {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
}

// fakeOutbox 记录所有写入的事件
type fakeOutbox struct {
	mu     sync.Mutex
	events []outboxRecord
}

type outboxRecord struct {
	aggregateType string
	eventType     string
	routingKey    string
	aggregateID   uint
	payload       PaymentConfirmed
}

func (o *fakeOutbox) Insert(aggregateType, eventType, routingKey string, aggregateID uint, payload any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	body, _ := json.Marshal(payload)
	var pc PaymentConfirmed
	_ = json.Unmarshal(body, &pc)
	o.events = append(o.events, outboxRecord{
		aggregateType: aggregateType,
		eventType:     eventType,
		routingKey:    routingKey,
		aggregateID:   aggregateID,
		payload:       pc,
	})
	return nil
}

func (o *fakeOutbox) snapshot() []outboxRecord {
	o.mu.Lock()
	defer o.mu.Unlock()
	cp := make([]outboxRecord, len(o.events))
	copy(cp, o.events)
	return cp
}

// fakeRedis 内存版，实现 listener 需要的三个命令。
// 用 SetNX 做幂等判定，单测要能区分首次 / 重复写入。
type fakeRedis struct {
	mu sync.Mutex
	kv map[string]string
}

func newFakeRedis() *fakeRedis { return &fakeRedis{kv: map[string]string{}} }

func (r *fakeRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := redis.NewStringCmd(ctx)
	if v, ok := r.kv[key]; ok {
		c.SetVal(v)
	} else {
		c.SetErr(redis.Nil)
	}
	return c
}
func (r *fakeRedis) Set(ctx context.Context, key string, value any, ttl time.Duration) *redis.StatusCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := redis.NewStatusCmd(ctx)
	r.kv[key] = toStr(value)
	c.SetVal("OK")
	return c
}
func (r *fakeRedis) SetNX(ctx context.Context, key string, value any, ttl time.Duration) *redis.BoolCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := redis.NewBoolCmd(ctx)
	if _, ok := r.kv[key]; ok {
		c.SetVal(false)
		return c
	}
	r.kv[key] = toStr(value)
	c.SetVal(true)
	return c
}

func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case uint64:
		return strconv.FormatUint(x, 10)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	default:
		return ""
	}
}

// 构造一条编码良好的 PaymentConfirmed log
func mustEncodeLog(t *testing.T, escrow common.Address, orderID common.Hash, buyer common.Address, amount *big.Int, block uint64, tx common.Hash, idx uint) types.Log {
	t.Helper()
	parsed, err := abi.JSON(strings.NewReader(paymentConfirmedABI))
	if err != nil {
		t.Fatalf("abi parse: %v", err)
	}
	data, err := parsed.Events["PaymentConfirmed"].Inputs.NonIndexed().Pack(buyer, amount)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return types.Log{
		Address:     escrow,
		Topics:      []common.Hash{EventTopic(), orderID},
		Data:        data,
		BlockNumber: block,
		TxHash:      tx,
		Index:       idx,
	}
}

func init() {
	// listener.go 里 util.LogrusObj 是包级 logger，需要先初始化才能避免 nil 解引用
	log.InitLog()
}

func TestStartPaymentListener_NoEnvSkips(t *testing.T) {
	t.Setenv(envRPCURL, "")
	t.Setenv(envEscrowAddr, "")
	if err := StartPaymentListener(context.Background()); err != nil {
		t.Fatalf("expected nil when env missing, got %v", err)
	}
}

func TestStartPaymentListener_InvalidAddress(t *testing.T) {
	t.Setenv(envRPCURL, "http://localhost:8545")
	t.Setenv(envEscrowAddr, "not-an-address")
	err := StartPaymentListener(context.Background())
	if err == nil {
		t.Fatalf("expected error for invalid address")
	}
}

func TestStartPaymentListener_CatchUpAndSubscribe(t *testing.T) {
	escrow := common.HexToAddress("0x0000000000000000000000000000000000000abc")
	t.Setenv(envRPCURL, "http://stub")
	t.Setenv(envEscrowAddr, escrow.Hex())

	orderID1 := common.HexToHash("0x01")
	buyer1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	historic := mustEncodeLog(t, escrow, orderID1, buyer1, big.NewInt(1000), 9, common.HexToHash("0xaa"), 0)

	client := newFakeClient(10, []types.Log{historic})
	rdb := newFakeRedis()
	// 预置 last_block = 8，意味着重启时漏掉了 block 9 这条 PaymentConfirmed，要靠 catch-up 补
	rdb.Set(context.Background(), lastBlockKey, "8", 0)
	ob := &fakeOutbox{}

	opts := listenerOpts{
		dial: func(ctx context.Context, url string) (ethClient, error) { return client, nil },
		outbox: func(ctx context.Context) outboxWriter {
			return ob
		},
		rdb: func() redisCmd { return rdb },
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := startPaymentListener(ctx, opts); err != nil {
		t.Fatalf("start: %v", err)
	}

	// 等订阅建立
	deadline := time.Now().Add(2 * time.Second)
	for client.subscribeCh == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if client.subscribeCh == nil {
		t.Fatalf("subscription never opened")
	}

	// catch-up 完成后应已经写入第一条
	waitFor(t, func() bool { return len(ob.snapshot()) == 1 }, time.Second, "catch-up event")

	// 推一条新事件
	orderID2 := common.HexToHash("0x02")
	buyer2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	live := mustEncodeLog(t, escrow, orderID2, buyer2, big.NewInt(2500), 11, common.HexToHash("0xbb"), 0)
	client.subscribeCh <- live

	waitFor(t, func() bool { return len(ob.snapshot()) == 2 }, time.Second, "live event")

	// 再推一条重复（同 tx + logIndex）应被 dedupe 吃掉
	client.subscribeCh <- live
	time.Sleep(80 * time.Millisecond)
	if got := len(ob.snapshot()); got != 2 {
		t.Fatalf("duplicate should be deduped, want 2 events, got %d", got)
	}

	events := ob.snapshot()
	if events[0].routingKey != "web3.payment.confirmed" {
		t.Errorf("routingKey want web3.payment.confirmed got %s", events[0].routingKey)
	}
	if events[0].eventType != "PaymentConfirmed" || events[0].aggregateType != "web3_payment" {
		t.Errorf("event metadata mismatch: %+v", events[0])
	}
	if events[0].payload.OrderID != orderID1.Hex() {
		t.Errorf("event 0 orderID want %s got %s", orderID1.Hex(), events[0].payload.OrderID)
	}
	if events[0].payload.Amount != "1000" {
		t.Errorf("event 0 amount want 1000 got %s", events[0].payload.Amount)
	}
	if !strings.EqualFold(events[1].payload.Buyer, buyer2.Hex()) {
		t.Errorf("event 1 buyer want %s got %s", buyer2.Hex(), events[1].payload.Buyer)
	}
	if events[1].payload.BlockNumber != 11 {
		t.Errorf("event 1 block want 11 got %d", events[1].payload.BlockNumber)
	}

	// last_block 应推进到最新 log 的 block
	val, err := rdb.Get(ctx, lastBlockKey).Result()
	if err != nil {
		t.Fatalf("read last_block: %v", err)
	}
	if val != "11" {
		t.Errorf("last_block want 11 got %s", val)
	}
}

func TestStartPaymentListener_ReorgRemovedSkipped(t *testing.T) {
	escrow := common.HexToAddress("0x00000000000000000000000000000000000000ee")
	t.Setenv(envRPCURL, "http://stub")
	t.Setenv(envEscrowAddr, escrow.Hex())

	client := newFakeClient(5, nil)
	rdb := newFakeRedis()
	ob := &fakeOutbox{}

	opts := listenerOpts{
		dial:   func(ctx context.Context, url string) (ethClient, error) { return client, nil },
		outbox: func(ctx context.Context) outboxWriter { return ob },
		rdb:    func() redisCmd { return rdb },
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := startPaymentListener(ctx, opts); err != nil {
		t.Fatalf("start: %v", err)
	}

	waitFor(t, func() bool { return client.subscribeCh != nil }, time.Second, "subscribe ready")

	orderID := common.HexToHash("0x03")
	buyer := common.HexToAddress("0x3333333333333333333333333333333333333333")
	removed := mustEncodeLog(t, escrow, orderID, buyer, big.NewInt(7), 6, common.HexToHash("0xcc"), 1)
	removed.Removed = true
	client.subscribeCh <- removed

	time.Sleep(80 * time.Millisecond)
	if got := len(ob.snapshot()); got != 0 {
		t.Fatalf("removed log should be skipped, got %d events", got)
	}
}

func waitFor(t *testing.T, cond func() bool, max time.Duration, desc string) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", desc)
}
