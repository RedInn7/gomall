package web3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/web3/escrow"
)

var testEscrow = common.HexToAddress("0x0000000000000000000000000000000000000e5c")

func init() {
	// listener 后台 goroutine 会打日志，包级 logger 必须先就位
	util.LogrusObj = logrus.New()
	util.LogrusObj.Out = io.Discard
}

type fakeSub struct{}

func (fakeSub) Unsubscribe()      {}
func (fakeSub) Err() <-chan error { return nil }

type fakeClient struct {
	mu       sync.Mutex
	chainID  int64
	head     uint64
	logs     []types.Log
	subErr   error       // SubscribeFilterLogs 返回的错误，HTTP RPC 下是 rpc.ErrNotificationsUnsupported
	failFrom uint64      // 非 0 时，from >= failFrom 的 FilterLogs 返回错误
	queries  [][2]uint64 // 每次 FilterLogs 的 [from, to]
}

func (c *fakeClient) ChainID(context.Context) (*big.Int, error) {
	return big.NewInt(c.chainID), nil
}

func (c *fakeClient) BlockNumber(context.Context) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.head, nil
}

func (c *fakeClient) FilterLogs(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, [2]uint64{q.FromBlock.Uint64(), q.ToBlock.Uint64()})
	if c.failFrom != 0 && q.FromBlock.Uint64() >= c.failFrom {
		return nil, errors.New("query returned more than 10000 results")
	}
	var out []types.Log
	for _, lg := range c.logs {
		if lg.BlockNumber >= q.FromBlock.Uint64() && lg.BlockNumber <= q.ToBlock.Uint64() {
			out = append(out, lg)
		}
	}
	return out, nil
}

func (c *fakeClient) SubscribeFilterLogs(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
	if c.subErr != nil {
		return nil, c.subErr
	}
	return fakeSub{}, nil
}

func (c *fakeClient) Close() {}

type fakeRedis struct {
	mu     sync.Mutex
	kv     map[string]string
	getErr error // 非 nil 时 Get 一律返回它，模拟 Redis 故障
}

func newFakeRedis() *fakeRedis { return &fakeRedis{kv: map[string]string{}} }

func (r *fakeRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd := redis.NewStringCmd(ctx)
	if r.getErr != nil {
		cmd.SetErr(r.getErr)
	} else if v, ok := r.kv[key]; ok {
		cmd.SetVal(v)
	} else {
		cmd.SetErr(redis.Nil)
	}
	return cmd
}

func (r *fakeRedis) Set(ctx context.Context, key string, value any, _ time.Duration) *redis.StatusCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kv[key] = fmt.Sprint(value)
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetVal("OK")
	return cmd
}

func (r *fakeRedis) SetNX(ctx context.Context, key string, value any, _ time.Duration) *redis.BoolCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.kv[key]
	if !exists {
		r.kv[key] = fmt.Sprint(value)
	}
	cmd := redis.NewBoolCmd(ctx)
	cmd.SetVal(!exists)
	return cmd
}

func (r *fakeRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for _, k := range keys {
		if _, ok := r.kv[k]; ok {
			delete(r.kv, k)
			n++
		}
	}
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(n)
	return cmd
}

func (r *fakeRedis) value(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.kv[key]
}

type fakeOutbox struct {
	mu       sync.Mutex
	events   []PaymentConfirmed
	failNext int // 接下来这么多次 Insert 返回错误，模拟 DB 故障
}

func (o *fakeOutbox) Insert(_, _, _ string, _ uint, payload any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failNext > 0 {
		o.failNext--
		return errors.New("db down")
	}
	ev, ok := payload.(PaymentConfirmed)
	if !ok {
		return fmt.Errorf("unexpected payload %T", payload)
	}
	o.events = append(o.events, ev)
	return nil
}

func (o *fakeOutbox) snapshot() []PaymentConfirmed {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]PaymentConfirmed(nil), o.events...)
}

// escrowLog 按 pkg/web3/escrow 里的合约 ABI 编码一条 PaymentConfirmed 日志：
// orderID、buyer 是 indexed，进 topics；amount 进 data。
func escrowLog(t *testing.T, orderID common.Hash, buyer common.Address, amount *big.Int, block uint64, tx common.Hash) types.Log {
	t.Helper()
	parsed, err := abi.JSON(strings.NewReader(escrow.EscrowABI))
	if err != nil {
		t.Fatal(err)
	}
	event := parsed.Events["PaymentConfirmed"]
	data, err := event.Inputs.NonIndexed().Pack(amount)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := abi.MakeTopics([]any{orderID}, []any{buyer})
	if err != nil {
		t.Fatal(err)
	}
	return types.Log{
		Address:     testEscrow,
		Topics:      []common.Hash{event.ID, indexed[0][0], indexed[1][0]},
		Data:        data,
		BlockNumber: block,
		TxHash:      tx,
	}
}

// newTestListener 直接构造 listener，便于逐轮调用 scanConfirmed 断言水位
func newTestListener(t *testing.T, rdb *fakeRedis, ob *fakeOutbox) *listener {
	t.Helper()
	parsed, err := abi.JSON(strings.NewReader(escrow.EscrowABI))
	if err != nil {
		t.Fatal(err)
	}
	return &listener{
		escrow:       testEscrow,
		abi:          parsed,
		topic:        parsed.Events["PaymentConfirmed"].ID,
		confirmDepth: 2,
		outbox:       func(context.Context) outboxWriter { return ob },
		rdb:          func() redisCmd { return rdb },
		backoff:      newBackoff(time.Second, time.Minute),
	}
}

func startTestListener(t *testing.T, client ethClient, rdb *fakeRedis, ob *fakeOutbox) {
	t.Helper()
	t.Setenv(envRPCURL, "http://stub")
	t.Setenv(envEscrowAddr, testEscrow.Hex())
	t.Setenv(envConfirmDepth, "2")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	err := startPaymentListener(ctx, listenerOpts{
		dial:   func(context.Context, string) (ethClient, error) { return client, nil },
		outbox: func(context.Context) outboxWriter { return ob },
		rdb:    func() redisCmd { return rdb },
	})
	if err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", desc)
}

func TestListenerDecodesEscrowLogAndSkipsMalformedOne(t *testing.T) {
	orderID := common.HexToHash("0x0a")
	buyer := common.HexToAddress("0x1111111111111111111111111111111111111111")
	amount, _ := new(big.Int).SetString("1500000000000000000", 10)
	good := escrowLog(t, orderID, buyer, amount, 6, common.HexToHash("0xbb"))

	// 旧 listener 期待的布局：buyer 不在 topic 而在 data。真实合约不会这样发，解不出来就应跳过。
	malformed := escrowLog(t, common.HexToHash("0x0b"), buyer, big.NewInt(1), 5, common.HexToHash("0xaa"))
	malformed.Topics = malformed.Topics[:2]

	client := &fakeClient{head: 10, logs: []types.Log{malformed, good}} // 确认深度 2 → safeHead=8
	rdb := newFakeRedis()
	rdb.kv[lastBlockKey] = "4"
	ob := &fakeOutbox{}
	startTestListener(t, client, rdb, ob)

	waitFor(t, func() bool { return rdb.value(lastBlockKey) == "8" }, "watermark passes the malformed log")
	events := ob.snapshot()
	if len(events) != 1 {
		t.Fatalf("want only the well-formed event, got %+v", events)
	}
	want := PaymentConfirmed{OrderID: orderID.Hex(), Buyer: buyer.Hex(), Amount: amount.String(), TxHash: good.TxHash.Hex(), BlockNumber: 6}
	if events[0] != want {
		t.Fatalf("decoded event mismatch:\n got %+v\nwant %+v", events[0], want)
	}
}

func TestOutboxFailureReleasesDedupeKeyForRescan(t *testing.T) {
	ctx := context.Background()
	lg := escrowLog(t, common.HexToHash("0x0c"), common.HexToAddress("0x2222222222222222222222222222222222222222"), big.NewInt(7), 6, common.HexToHash("0xcc"))
	client := &fakeClient{head: 10, logs: []types.Log{lg}}
	rdb := newFakeRedis()
	rdb.kv[lastBlockKey] = "4"
	ob := &fakeOutbox{failNext: 1}
	l := newTestListener(t, rdb, ob)

	if err := l.scanConfirmed(ctx, client); err != nil {
		t.Fatal(err)
	}
	if got := rdb.value(lastBlockKey); got != "5" {
		t.Fatalf("watermark must stop before the failed block, got %q", got)
	}
	if err := l.scanConfirmed(ctx, client); err != nil {
		t.Fatal(err)
	}
	if events := ob.snapshot(); len(events) != 1 || events[0].TxHash != lg.TxHash.Hex() {
		t.Fatalf("rescan must write the event once the dedupe key is released, got %+v", events)
	}
	if got := rdb.value(lastBlockKey); got != "8" {
		t.Fatalf("watermark want 8 after the retry succeeds, got %q", got)
	}
}

func TestCursorReadErrorSkipsRoundInsteadOfColdStart(t *testing.T) {
	lg := escrowLog(t, common.HexToHash("0x0d"), common.HexToAddress("0x3333333333333333333333333333333333333333"), big.NewInt(9), 6, common.HexToHash("0xdd"))
	client := &fakeClient{head: 10, logs: []types.Log{lg}}
	rdb := newFakeRedis()
	rdb.getErr = errors.New("redis: connection refused")
	ob := &fakeOutbox{}
	l := newTestListener(t, rdb, ob)

	if err := l.scanConfirmed(context.Background(), client); err == nil {
		t.Fatal("cursor read error must abort the round")
	}
	// 冷启动会从 safeHead=8 起扫并写下水位 8，block 6 的付款就永远被跳过
	if len(client.queries) != 0 || rdb.value(lastBlockKey) != "" || len(ob.snapshot()) != 0 {
		t.Fatalf("round must not scan or move the cursor: queries=%v cursor=%q", client.queries, rdb.value(lastBlockKey))
	}
}

func TestListenerPollsWhenRPCCannotSubscribe(t *testing.T) {
	lg := escrowLog(t, common.HexToHash("0x0e"), common.HexToAddress("0x4444444444444444444444444444444444444444"), big.NewInt(3), 6, common.HexToHash("0xee"))
	client := &fakeClient{head: 10, logs: []types.Log{lg}, subErr: rpc.ErrNotificationsUnsupported}
	rdb := newFakeRedis()
	rdb.kv[lastBlockKey] = "4"
	ob := &fakeOutbox{}
	startTestListener(t, client, rdb, ob)

	waitFor(t, func() bool { return len(ob.snapshot()) == 1 && rdb.value(lastBlockKey) == "8" }, "scan over HTTP RPC without subscription")
}

func TestScanCatchesUpInChunksAndKeepsFinishedChunks(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{head: 5002, failFrom: 2002} // safeHead=5000，第二片查询失败
	rdb := newFakeRedis()
	rdb.kv[lastBlockKey] = "1"
	l := newTestListener(t, rdb, &fakeOutbox{})

	if err := l.scanConfirmed(ctx, client); err == nil {
		t.Fatal("want the failed chunk to surface an error")
	}
	if got := rdb.value(lastBlockKey); got != "2001" {
		t.Fatalf("watermark must keep the finished chunk, got %q", got)
	}

	client.failFrom = 0
	if err := l.scanConfirmed(ctx, client); err != nil {
		t.Fatal(err)
	}
	want := [][2]uint64{{2, 2001}, {2002, 4001}, {2002, 4001}, {4002, 5000}}
	if fmt.Sprint(client.queries) != fmt.Sprint(want) {
		t.Fatalf("chunked queries mismatch: got %v want %v", client.queries, want)
	}
	if got := rdb.value(lastBlockKey); got != "5000" {
		t.Fatalf("watermark want 5000, got %q", got)
	}
}

func TestListenerStopsOnChainIDMismatch(t *testing.T) {
	client := &fakeClient{chainID: 11155111, head: 10}
	l := newTestListener(t, newFakeRedis(), &fakeOutbox{})
	l.chainID = 1
	l.dial = func(context.Context, string) (ethClient, error) { return client, nil }

	done := make(chan struct{})
	go func() { l.run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("listener must exit instead of reconnecting to the wrong chain")
	}
	if len(client.queries) != 0 {
		t.Fatalf("must not scan the wrong chain, queries=%v", client.queries)
	}
}

func TestBackoffKeepsGrowingWhenInitialScanFails(t *testing.T) {
	rdb := newFakeRedis()
	rdb.getErr = errors.New("redis: connection refused")
	l := newTestListener(t, rdb, &fakeOutbox{})
	l.dial = func(context.Context, string) (ethClient, error) { return &fakeClient{head: 10}, nil }
	l.backoff.cur = 8 * time.Second

	if err := l.connectAndServe(context.Background()); err == nil {
		t.Fatal("initial scan must fail while redis is down")
	}
	if l.backoff.cur != 8*time.Second {
		t.Fatalf("backoff reset before the first successful scan: %s", l.backoff.cur)
	}
}
