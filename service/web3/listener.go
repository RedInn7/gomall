package web3

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/internal/shared/outbox"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/web3/escrow"
	"github.com/RedInn7/gomall/repository/cache"
)

const (
	envRPCURL       = "WEB3_RPC_URL"
	envEscrowAddr   = "WEB3_ESCROW_ADDR"
	envConfirmDepth = "WEB3_CONFIRM_DEPTH"
	lastBlockKey    = "web3:listener:last_block"
	eventDedupeTTL  = 72 * time.Hour
	subscribeBuffer = 64
	// defaultConfirmDepth 默认确认深度：事件所在区块需被埋够这么多个后续区块才入账，
	// 抵御链重组（reorg）撤销已确认 log 导致的错误放货。以太坊主网 12 个确认是常用安全值。
	defaultConfirmDepth = 12
	// pollInterval 轮询链头推进确认水位的间隔。轮询而非纯订阅，
	// 是为了让“未达确认深度先不处理”的 log 能在后续区块到达后被重新扫到。
	pollInterval = 12 * time.Second
)

// errMalformedLog 标记日志本身解不出来的永久错误：重扫同一条日志结果不会变，
// 告警后跳过即可；若当成临时错误卡住水位，之后所有链上付款都无法入账。
var errMalformedLog = errors.New("malformed PaymentConfirmed log")

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
	return outbox.NewOutboxDao(ctx)
}

// redisCmd 抽象 listener 用到的 redis 命令，单测里换成 miniredis 或内存替身
type redisCmd interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

var defaultRedis = func() redisCmd { return cache.RedisClient }

// PaymentConfirmed 是 listener 内部解码后的事件 payload，原样落到 outbox JSON 里。
type PaymentConfirmed struct {
	OrderID     string `json:"order_id"` // 0x 开头的 bytes32 hex，与合约内 orderID 字段一致
	Buyer       string `json:"buyer"`    // EOA 地址
	Amount      string `json:"amount"`   // wei 数值，string 形式避免 JSON 精度损失
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
	escrowAddr := strings.TrimSpace(os.Getenv(envEscrowAddr))
	if rpcURL == "" || escrowAddr == "" {
		util.LogrusObj.Infoln("Web3 listener: WEB3_RPC_URL / WEB3_ESCROW_ADDR 未配置，跳过启动")
		return nil
	}
	if !common.IsHexAddress(escrowAddr) {
		return fmt.Errorf("invalid escrow address %q", escrowAddr)
	}

	// 直接用合约构建产物里的 ABI，不再内嵌一份手抄版本，避免和 Escrow.sol 再次漂移
	parsedABI, err := abi.JSON(strings.NewReader(escrow.EscrowABI))
	if err != nil {
		return fmt.Errorf("parse escrow abi: %w", err)
	}
	event, ok := parsedABI.Events["PaymentConfirmed"]
	if !ok {
		return errors.New("escrow abi has no PaymentConfirmed event")
	}

	l := &listener{
		rpcURL:       rpcURL,
		escrow:       common.HexToAddress(escrowAddr),
		abi:          parsedABI,
		topic:        event.ID,
		confirmDepth: confirmDepth(),
		dial:         opts.dial,
		outbox:       opts.outbox,
		rdb:          opts.rdb,
		backoff:      newBackoff(time.Second, time.Minute),
	}
	go l.run(ctx)
	util.LogrusObj.Infof("Web3 listener started escrow=%s confirmDepth=%d", escrowAddr, l.confirmDepth)
	return nil
}

// confirmDepth 读取 WEB3_CONFIRM_DEPTH 确认深度，缺省 / 非法时回落到安全默认值。
// 设为 0 等于关闭确认深度保护（不推荐），因此非正值统一回落到默认值。
func confirmDepth() uint64 {
	if v, err := strconv.ParseUint(strings.TrimSpace(os.Getenv(envConfirmDepth)), 10, 64); err == nil && v > 0 {
		return v
	}
	return defaultConfirmDepth
}

type listener struct {
	rpcURL       string
	escrow       common.Address
	abi          abi.ABI
	topic        common.Hash
	confirmDepth uint64
	dial         ethDialer
	outbox       func(ctx context.Context) outboxWriter
	rdb          func() redisCmd
	backoff      *backoff
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

	// 订阅仅作为“链头推进”的快速唤醒信号，真正的事件处理一律走 scanConfirmed 的
	// FilterLogs 按确认深度回扫；不直接吃订阅推来的 log——它们多半尚未埋够确认数，
	// 直接入账会被 reorg 撤销造成错误放货。订阅 + 定时器双触发，确保链头推进后及时回扫。
	logsCh := make(chan types.Log, subscribeBuffer)
	sub, err := client.SubscribeFilterLogs(ctx, l.query(big.NewInt(int64(head+1)), nil), logsCh)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Unsubscribe()
	l.backoff.reset()

	// 首次连接立即回扫一轮已确认区间（补停机期间漏掉且已埋够确认深度的事件）。
	if err := l.scanConfirmed(ctx, client); err != nil {
		return fmt.Errorf("initial scan: %w", err)
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-sub.Err():
			if err == nil {
				return errors.New("subscription closed")
			}
			return err
		case <-logsCh:
			// 订阅推来的 log 仅用于唤醒回扫；丢弃 log 本体，由 scanConfirmed 统一按
			// 确认深度处理（去重键保证不会漏处理也不会重复入账）。
			if err := l.scanConfirmed(ctx, client); err != nil {
				util.LogrusObj.Warnf("Web3 listener scan(on-log) err=%v", err)
			}
		case <-ticker.C:
			if err := l.scanConfirmed(ctx, client); err != nil {
				util.LogrusObj.Warnf("Web3 listener scan(tick) err=%v", err)
			}
		}
	}
}

// scanConfirmed 把已埋够确认深度的区间 [last_block+1, head-confirmDepth] 内的事件回扫入账。
//   - safeHead = head - confirmDepth：低于此高度的区块才视为“已确认”，可安全入账；
//     未达确认深度的区块本轮不处理，等后续区块到达、链头推进后再被扫到。
//   - watermark 只在某区块内所有 log 均成功处理后才推进并持久化，处理失败的 log
//     下一轮会从 last_block+1 重新扫描，配合去重键（tx+logIndex）兜底，不丢不重。
func (l *listener) scanConfirmed(ctx context.Context, client ethClient) error {
	head, err := client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get head block: %w", err)
	}
	if head < l.confirmDepth {
		return nil // 链头尚浅，还没有任何区块达到确认深度
	}
	safeHead := head - l.confirmDepth

	last, err := l.loadLastBlock(ctx)
	if err != nil {
		return fmt.Errorf("load last block: %w", err)
	}
	if last > safeHead {
		// 游标已领先安全高度（如曾配置更小确认深度或链回退），不回退游标，等链头追上。
		return nil
	}
	from := last + 1
	if last == 0 {
		// 冷启动无游标：从安全高度起步，不回放整条链历史。
		from = safeHead
	}
	if from > safeHead {
		return nil
	}

	q := l.query(new(big.Int).SetUint64(from), new(big.Int).SetUint64(safeHead))
	logs, err := client.FilterLogs(ctx, q)
	if err != nil {
		return fmt.Errorf("filter logs [%d,%d]: %w", from, safeHead, err)
	}

	// failedFromBlock 记录首个处理失败的区块号，watermark 不得越过它，
	// 保证失败区块下一轮仍会被重新扫描。
	var failedFromBlock uint64
	for i := range logs {
		lg := logs[i]
		if lg.Removed {
			// 已确认深度内仍标记 Removed 极少见（深 reorg），跳过即可，去重键兜底。
			continue
		}
		if failedFromBlock != 0 && lg.BlockNumber >= failedFromBlock {
			continue
		}
		if err := l.handleLog(ctx, lg); err != nil {
			if errors.Is(err, errMalformedLog) {
				// 解码类错误重扫也不会好：记 error 留给人工核对后跳过，不卡水位
				util.LogrusObj.Errorf("Web3 listener skip malformed log tx=%s logIdx=%d block=%d err=%v", lg.TxHash.Hex(), lg.Index, lg.BlockNumber, err)
				continue
			}
			util.LogrusObj.Errorf("Web3 listener handle log tx=%s logIdx=%d block=%d err=%v", lg.TxHash.Hex(), lg.Index, lg.BlockNumber, err)
			if failedFromBlock == 0 || lg.BlockNumber < failedFromBlock {
				failedFromBlock = lg.BlockNumber
			}
		}
	}

	newWatermark := safeHead
	if failedFromBlock != 0 && failedFromBlock-1 < newWatermark {
		newWatermark = failedFromBlock - 1
	}
	if newWatermark >= from {
		l.saveLastBlock(ctx, newWatermark)
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
	// 校验事件确实来自配置的 escrow 合约地址。FilterQuery 已按地址过滤，
	// 这里再做一次防御性核对，杜绝任意合约伪造同名事件冒充结算来源。
	if lg.Address != l.escrow {
		return fmt.Errorf("%w: event from unexpected contract %s, want %s", errMalformedLog, lg.Address.Hex(), l.escrow.Hex())
	}
	// 同理校验事件签名 topic，避免地址匹配但事件类型不符的脏数据。
	if len(lg.Topics) == 0 || lg.Topics[0] != l.topic {
		return fmt.Errorf("%w: unexpected event topic", errMalformedLog)
	}
	// 合约里 orderID、buyer 都是 indexed，分别在 topics[1]、topics[2]；data 里只有 amount
	if len(lg.Topics) != 3 {
		return fmt.Errorf("%w: topics=%d", errMalformedLog, len(lg.Topics))
	}
	orderID := lg.Topics[1]
	buyer := common.BytesToAddress(lg.Topics[2].Bytes())
	values, err := l.abi.Unpack("PaymentConfirmed", lg.Data)
	if err != nil {
		return fmt.Errorf("%w: unpack data: %v", errMalformedLog, err)
	}
	if len(values) != 1 {
		return fmt.Errorf("%w: unexpected non-indexed args: %d", errMalformedLog, len(values))
	}
	amount, ok := values[0].(*big.Int)
	if !ok || amount == nil {
		return fmt.Errorf("%w: amount type assertion failed", errMalformedLog)
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
	claimed, err := l.tryClaim(ctx, dedupeKey)
	if err != nil {
		util.LogrusObj.Warnf("Web3 listener dedupe redis err=%v key=%s, fallback to write", err, dedupeKey)
	} else if !claimed {
		return nil
	}

	if err := l.outbox(ctx).Insert(
		"web3_payment",
		"PaymentConfirmed",
		"web3.payment.confirmed",
		0,
		ev,
	); err != nil {
		// 去重 key 已占位但事件没落库：不撤销的话，下一轮重扫会把它当成已处理跳过，事件就永久丢了
		if claimed {
			l.releaseClaim(ctx, dedupeKey)
		}
		return fmt.Errorf("outbox insert: %w", err)
	}
	return nil
}

// releaseClaim 撤销 tryClaim 的占位。用不随调用方取消的 ctx：outbox 失败常常正是 ctx 被取消，
// DEL 不能跟着一起失败。
func (l *listener) releaseClaim(ctx context.Context, key string) {
	rdb := l.rdb()
	if rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := rdb.Del(ctx, key).Err(); err != nil {
		util.LogrusObj.Errorf("Web3 listener release dedupe key=%s err=%v", key, err)
	}
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

// loadLastBlock 读取游标，返回 0 表示没有游标（冷启动）。
// 只有 key 不存在（redis.Nil）才算没有游标；其他错误必须上抛，
// 否则 Redis 一抖就会被当成冷启动，从 safeHead 起扫而跳过中间的块。
func (l *listener) loadLastBlock(ctx context.Context) (uint64, error) {
	rdb := l.rdb()
	if rdb == nil {
		return 0, nil
	}
	v, err := rdb.Get(ctx, lastBlockKey).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", lastBlockKey, v, err)
	}
	return n, nil
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
