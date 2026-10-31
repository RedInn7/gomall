package web3

import (
	"context"
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
	mu   sync.Mutex
	head uint64
	logs []types.Log
}

func (c *fakeClient) BlockNumber(context.Context) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.head, nil
}

func (c *fakeClient) FilterLogs(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []types.Log
	for _, lg := range c.logs {
		if lg.BlockNumber >= q.FromBlock.Uint64() && lg.BlockNumber <= q.ToBlock.Uint64() {
			out = append(out, lg)
		}
	}
	return out, nil
}

func (c *fakeClient) SubscribeFilterLogs(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error) {
	return fakeSub{}, nil
}

func (c *fakeClient) Close() {}

type fakeRedis struct {
	mu sync.Mutex
	kv map[string]string
}

func newFakeRedis() *fakeRedis { return &fakeRedis{kv: map[string]string{}} }

func (r *fakeRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd := redis.NewStringCmd(ctx)
	if v, ok := r.kv[key]; ok {
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

func (r *fakeRedis) value(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.kv[key]
}

type fakeOutbox struct {
	mu     sync.Mutex
	events []PaymentConfirmed
}

func (o *fakeOutbox) Insert(_, _, _ string, _ uint, payload any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
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
