package web3

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/go-redis/v9"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

const (
	envRPCURL       = "WEB3_RPC_URL"
	envEscrowAddr   = "WEB3_ESCROW_ADDR"
	lastBlockKey    = "web3:listener:last_block"
	eventDedupeTTL  = 72 * time.Hour
	subscribeBuffer = 64
)

// paymentConfirmedABI 与 escrow 合约里定义的 PaymentConfirmed event 形参对齐。
// 解码用通用 ABI，避免依赖具体合约的 Go binding。
const paymentConfirmedABI = `[{
  "anonymous": false,
  "inputs": [
    {"indexed": true,  "internalType": "bytes32", "name": "orderID", "type": "bytes32"},
    {"indexed": false, "internalType": "address", "name": "buyer",   "type": "address"},
    {"indexed": false, "internalType": "uint256", "name": "amount",  "type": "uint256"}
  ],
  "name": "PaymentConfirmed",
  "type": "event"
}]`

// ethClient 抽象出 listener 用到的 ethclient.Client 子集，便于单测注入 mock。
type ethClient interface {
	BlockNumber(ctx context.Context) (uint64, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error)
	Close()
}

// ethDialer 抽象 RPC 拨号，单测里替换成 mock 直接返回内存 client
type ethDialer func(ctx context.Context, rawurl string) (ethClient, error)

// 默认拨号器，包一层使返回值满足 ethClient 接口
var defaultDialer ethDialer = func(ctx context.Context, rawurl string) (ethClient, error) {
	c, err := ethclient.DialContext(ctx, rawurl)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// outboxWriter 抽象 outbox 写入，单测断言事件 payload
type outboxWriter interface {
	Insert(aggregateType, eventType, routingKey string, aggregateID uint, payload any) error
}

var defaultOutbox = func(ctx context.Context) outboxWriter {
	return dao.NewOutboxDao(ctx)
}

// redisCmd 抽象 listener 用到的 redis 命令，单测里换成 miniredis 或内存替身
type redisCmd interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd
}

var defaultRedis = func() redisCmd { return cache.RedisClient }

// PaymentConfirmed 是 listener 内部解码后的事件 payload，原样落到 outbox JSON 里。
type PaymentConfirmed struct {
	OrderID     string `json:"order_id"`     // 0x 开头的 bytes32 hex，与合约内 orderID 字段一致
	Buyer       string `json:"buyer"`        // EOA 地址
	Amount      string `json:"amount"`       // wei 数值，string 形式避免 JSON 精度损失
	TxHash      string `json:"tx_hash"`
	LogIndex    uint   `json:"log_index"`
	BlockNumber uint64 `json:"block_number"`
}

// listenerOpts 让 StartPaymentListener 在测试里能注入替身
type listenerOpts struct {
	dial   ethDialer
	outbox func(ctx context.Context) outboxWriter
	rdb    func() redisCmd
}

func defaultOpts() listenerOpts {
	return listenerOpts{dial: defaultDialer, outbox: defaultOutbox, rdb: defaultRedis}
}

// StartPaymentListener 启动监听 escrow 合约的 PaymentConfirmed event。
// 缺任一关键 env (RPC_URL / ESCROW_ADDR) 时静默 return nil，让主进程继续启动。
// 启动后内部 goroutine 负责重连 + last_block 持久化 + catch-up，调用方只需要把进程 ctx 传进来。
func StartPaymentListener(ctx context.Context) error {
	return startPaymentListener(ctx, defaultOpts())
}

func startPaymentListener(ctx context.Context, opts listenerOpts) error {
	rpcURL := strings.TrimSpace(os.Getenv(envRPCURL))
	escrow := strings.TrimSpace(os.Getenv(envEscrowAddr))
	if rpcURL == "" || escrow == "" {
		util.LogrusObj.Infoln("Web3 listener: WEB3_RPC_URL / WEB3_ESCROW_ADDR 未配置，跳过启动")
		return nil
	}
	if !common.IsHexAddress(escrow) {
		return fmt.Errorf("invalid escrow address %q", escrow)
	}

	parsedABI, err := abi.JSON(strings.NewReader(paymentConfirmedABI))
	if err != nil {
		return fmt.Errorf("parse PaymentConfirmed abi: %w", err)
	}
	topic := crypto.Keccak256Hash([]byte("PaymentConfirmed(bytes32,address,uint256)"))

	l := &listener{
		rpcURL:   rpcURL,
		escrow:   common.HexToAddress(escrow),
		abi:      parsedABI,
		topic:    topic,
		dial:     opts.dial,
		outbox:   opts.outbox,
		rdb:      opts.rdb,
		backoff:  newBackoff(time.Second, time.Minute),
	}
	go l.run(ctx)
	util.LogrusObj.Infof("Web3 listener started escrow=%s", escrow)
	return nil
}

type listener struct {
	rpcURL  string
	escrow  common.Address
	abi     abi.ABI
	topic   common.Hash
	dial    ethDialer
	outbox  func(ctx context.Context) outboxWriter
	rdb     func() redisCmd
	backoff *backoff
}

// run 一直循环到 ctx.Done()，单次连接断开后按指数退避重连
func (l *listener) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := l.connectAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			util.LogrusObj.Warnf("Web3 listener disconnected: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		wait := l.backoff.next()
		util.LogrusObj.Infof("Web3 listener reconnect in %s", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (l *listener) connectAndServe(ctx context.Context) error {
	client, err := l.dial(ctx, l.rpcURL)
	if err != nil {
		return fmt.Errorf("dial rpc: %w", err)
	}
	defer client.Close()

	head, err := client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get head block: %w", err)
	}
	last := l.loadLastBlock(ctx)
	if last == 0 || last > head {
		last = head
	}
	// catch-up: 把停机期间漏掉的事件批量回放
	if last < head {
		if err := l.catchUp(ctx, client, last+1, head); err != nil {
			return fmt.Errorf("catch up: %w", err)
		}
	}
	l.saveLastBlock(ctx, head)

	// 订阅 head 之后的新事件
	logsCh := make(chan types.Log, subscribeBuffer)
	sub, err := client.SubscribeFilterLogs(ctx, l.query(big.NewInt(int64(head+1)), nil), logsCh)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Unsubscribe()
	l.backoff.reset()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sub.Err():
			if err == nil {
				return errors.New("subscription closed")
			}
			return err
		case lg := <-logsCh:
			if lg.Removed {
				// reorg 撤销的 log，依赖 idempotency key 兜底，跳过即可
				continue
			}
			if err := l.handleLog(ctx, lg); err != nil {
				util.LogrusObj.Errorf("Web3 listener handle log tx=%s logIdx=%d err=%v", lg.TxHash.Hex(), lg.Index, err)
				continue
			}
			l.saveLastBlock(ctx, lg.BlockNumber)
		}
	}
}

func (l *listener) catchUp(ctx context.Context, client ethClient, fromBlock, toBlock uint64) error {
	q := l.query(new(big.Int).SetUint64(fromBlock), new(big.Int).SetUint64(toBlock))
	logs, err := client.FilterLogs(ctx, q)
	if err != nil {
		return err
	}
	for i := range logs {
		lg := logs[i]
		if lg.Removed {
			continue
		}
		if err := l.handleLog(ctx, lg); err != nil {
			util.LogrusObj.Errorf("Web3 listener catch-up tx=%s logIdx=%d err=%v", lg.TxHash.Hex(), lg.Index, err)
		}
	}
	return nil
}

func (l *listener) query(from, to *big.Int) ethereum.FilterQuery {
	q := ethereum.FilterQuery{
		Addresses: []common.Address{l.escrow},
		Topics:    [][]common.Hash{{l.topic}},
	}
	if from != nil {
		q.FromBlock = from
	}
	if to != nil {
		q.ToBlock = to
	}
	return q
}

// handleLog 解码 + 幂等去重 + 写 outbox
func (l *listener) handleLog(ctx context.Context, lg types.Log) error {
	if len(lg.Topics) < 2 {
		return fmt.Errorf("malformed log: topics=%d", len(lg.Topics))
	}
	// indexed 参数走 topic，non-indexed 走 data
	orderID := lg.Topics[1]
	values, err := l.abi.Unpack("PaymentConfirmed", lg.Data)
	if err != nil {
		return fmt.Errorf("unpack data: %w", err)
	}
	if len(values) != 2 {
		return fmt.Errorf("unexpected non-indexed args: %d", len(values))
	}
	buyer, ok := values[0].(common.Address)
	if !ok {
		return fmt.Errorf("buyer type assertion failed")
	}
	amount, ok := values[1].(*big.Int)
	if !ok || amount == nil {
		return fmt.Errorf("amount type assertion failed")
	}

	ev := PaymentConfirmed{
		OrderID:     orderID.Hex(),
		Buyer:       buyer.Hex(),
		Amount:      amount.String(),
		TxHash:      lg.TxHash.Hex(),
		LogIndex:    lg.Index,
		BlockNumber: lg.BlockNumber,
	}

	dedupeKey := fmt.Sprintf("web3:event:%s:%d", lg.TxHash.Hex(), lg.Index)
	if first, err := l.tryClaim(ctx, dedupeKey); err != nil {
		util.LogrusObj.Warnf("Web3 listener dedupe redis err=%v key=%s, fallback to write", err, dedupeKey)
	} else if !first {
		return nil
	}

	if err := l.outbox(ctx).Insert(
		"web3_payment",
		"PaymentConfirmed",
		"web3.payment.confirmed",
		0,
		ev,
	); err != nil {
		return fmt.Errorf("outbox insert: %w", err)
	}
	return nil
}

// tryClaim 用 SETNX 占位，true 表示这是首次处理；redis 不可用时返回 err，由调用方决定降级策略
func (l *listener) tryClaim(ctx context.Context, key string) (bool, error) {
	rdb := l.rdb()
	if rdb == nil {
		return true, nil
	}
	ok, err := rdb.SetNX(ctx, key, "1", eventDedupeTTL).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (l *listener) loadLastBlock(ctx context.Context) uint64 {
	rdb := l.rdb()
	if rdb == nil {
		return 0
	}
	v, err := rdb.Get(ctx, lastBlockKey).Result()
	if err != nil {
		return 0
	}
	var n uint64
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return 0
	}
	return n
}

func (l *listener) saveLastBlock(ctx context.Context, block uint64) {
	rdb := l.rdb()
	if rdb == nil {
		return
	}
	if err := rdb.Set(ctx, lastBlockKey, block, 0).Err(); err != nil {
		util.LogrusObj.Warnf("Web3 listener persist last_block=%d err=%v", block, err)
	}
}

// EventTopic 暴露给外部测试 / 工具脚本算 topic
func EventTopic() common.Hash {
	return crypto.Keccak256Hash([]byte("PaymentConfirmed(bytes32,address,uint256)"))
}

// 简单指数退避，避开堆三方库
type backoff struct {
	cur, min, max time.Duration
}

func newBackoff(min, max time.Duration) *backoff {
	return &backoff{cur: min, min: min, max: max}
}

func (b *backoff) next() time.Duration {
	d := b.cur
	b.cur *= 2
	if b.cur > b.max {
		b.cur = b.max
	}
	return d
}

func (b *backoff) reset() { b.cur = b.min }
