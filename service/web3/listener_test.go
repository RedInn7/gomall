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
	mu           sync.Mutex
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

func (c *fakeClient) BlockNumber(ctx context.Context) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.head, nil
}

// setHead 推进链头，模拟新区块到达后此前的事件逐步埋够确认深度。
func (c *fakeClient) setHead(h uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.head = h
}

// appendLog 追加一条 FilterLogs 可返回的链上日志。
func (c *fakeClient) appendLog(lg types.Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filterLogs = append(c.filterLogs, lg)
}

// FilterLogs 按 [FromBlock, ToBlock] 过滤返回，贴近真实节点行为，
// 以便测试确认深度（safeHead）对回扫区间的影响。
func (c *fakeClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []types.Log
	for _, lg := range c.filterLogs {
		if q.FromBlock != nil && lg.BlockNumber < q.FromBlock.Uint64() {
			continue
		}
		if q.ToBlock != nil && lg.BlockNumber > q.ToBlock.Uint64() {
			continue
		}
		out = append(out, lg)
	}
	return out, nil
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

func TestStartPaymentListener_ConfirmDepthCatchUpAndScan(t *testing.T) {
	escrow := common.HexToAddress("0x0000000000000000000000000000000000000abc")
	t.Setenv(envRPCURL, "http://stub")
	t.Setenv(envEscrowAddr, escrow.Hex())
	// 确认深度设 3：block B 需 head >= B+3 才入账。
	t.Setenv(envConfirmDepth, "3")

	orderID1 := common.HexToHash("0x01")
	buyer1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	// 历史事件在 block 9；初始 head=12 → safeHead=9，已埋够 3 个确认，应被回扫入账。
	historic := mustEncodeLog(t, escrow, orderID1, buyer1, big.NewInt(1000), 9, common.HexToHash("0xaa"), 0)

	client := newFakeClient(12, []types.Log{historic})
	rdb := newFakeRedis()
	// 预置 last_block = 8，意味着重启时漏掉了 block 9 这条 PaymentConfirmed，要靠回扫补
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
	waitFor(t, func() bool { return client.subscribeCh != nil }, 2*time.Second, "subscription open")

	// 首轮回扫完成后应写入历史事件（block 9，已确认）
	waitFor(t, func() bool { return len(ob.snapshot()) == 1 }, time.Second, "confirmed catch-up event")

	// last_block 应推进到 safeHead=9（head 12 - depth 3），而非 head 本身
	waitFor(t, func() bool {
		v, _ := rdb.Get(ctx, lastBlockKey).Result()
		return v == "9"
	}, time.Second, "watermark advance to safeHead")

	// 追加一条 block 11 的新事件，此刻 head=12 → safeHead=9，未达确认深度，不应入账
	orderID2 := common.HexToHash("0x02")
	buyer2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	live := mustEncodeLog(t, escrow, orderID2, buyer2, big.NewInt(2500), 11, common.HexToHash("0xbb"), 0)
	client.appendLog(live)
	client.subscribeCh <- live // 仅触发回扫，本体丢弃
	time.Sleep(80 * time.Millisecond)
	if got := len(ob.snapshot()); got != 1 {
		t.Fatalf("unconfirmed event must not be settled, want 1 got %d", got)
	}

	// 链头推进到 14：block 11 现已埋够 3 个确认（safeHead=11），再触发回扫即应入账
	client.setHead(14)
	client.subscribeCh <- live
	waitFor(t, func() bool { return len(ob.snapshot()) == 2 }, time.Second, "now-confirmed live event")

	// 再次触发回扫，已处理事件靠去重键吃掉，不重复入账
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

	// last_block 应推进到最新 safeHead=11（head 14 - depth 3）
	waitFor(t, func() bool {
		v, _ := rdb.Get(ctx, lastBlockKey).Result()
		return v == "11"
	}, time.Second, "final watermark")
}

func TestStartPaymentListener_ReorgRemovedSkipped(t *testing.T) {
	escrow := common.HexToAddress("0x00000000000000000000000000000000000000ee")
	t.Setenv(envRPCURL, "http://stub")
	t.Setenv(envEscrowAddr, escrow.Hex())
	t.Setenv(envConfirmDepth, "2")

	orderID := common.HexToHash("0x03")
	buyer := common.HexToAddress("0x3333333333333333333333333333333333333333")
	// block 4，head=10 → safeHead=8，已确认；但标记 Removed（深 reorg 撤销），应被跳过不入账。
	removed := mustEncodeLog(t, escrow, orderID, buyer, big.NewInt(7), 4, common.HexToHash("0xcc"), 1)
	removed.Removed = true

	client := newFakeClient(10, []types.Log{removed})
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

	// 首轮回扫即覆盖 block 4；Removed log 应被跳过。
	time.Sleep(120 * time.Millisecond)
	if got := len(ob.snapshot()); got != 0 {
		t.Fatalf("removed log should be skipped, got %d events", got)
	}
}

// 校验事件来自非配置合约地址时被拒绝（防伪造来源）。
func TestHandleLog_RejectsForeignContract(t *testing.T) {
	escrow := common.HexToAddress("0x00000000000000000000000000000000000000ee")
	foreign := common.HexToAddress("0x00000000000000000000000000000000deadbeef")
	parsed, err := abi.JSON(strings.NewReader(paymentConfirmedABI))
	if err != nil {
		t.Fatalf("abi parse: %v", err)
	}
	rdb := newFakeRedis()
	ob := &fakeOutbox{}
	l := &listener{
		escrow:       escrow,
		abi:          parsed,
		topic:        EventTopic(),
		confirmDepth: 2,
		outbox:       func(ctx context.Context) outboxWriter { return ob },
		rdb:          func() redisCmd { return rdb },
	}
	buyer := common.HexToAddress("0x4444444444444444444444444444444444444444")
	lg := mustEncodeLog(t, foreign, common.HexToHash("0x05"), buyer, big.NewInt(1), 3, common.HexToHash("0xdd"), 0)
	if err := l.handleLog(context.Background(), lg); err == nil {
		t.Fatalf("expected rejection for foreign contract address")
	}
	if len(ob.snapshot()) != 0 {
		t.Fatalf("foreign-contract event must not be written to outbox")
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
