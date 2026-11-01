package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/rabbitmq"
)

// 异步下单：reserve 库存 -> 写 ticket -> 投递 MQ -> 立即返回 ticket
// 前端拿 ticket 轮询 GET /orders/status?ticket=...，consumer 落 DB 后写回结果。

const (
	OrderTicketTTL = time.Hour

	OrderTicketStatusPending = "pending"
	OrderTicketStatusOK      = "ok"
	OrderTicketStatusFailed  = "failed"
)

func OrderTicketKey(ticket string) string {
	return fmt.Sprintf("order:ticket:%s", ticket)
}

// AsyncOrderTask 投递到 MQ 的消息体。
// 不携带金额与卖家：单价 / boss 由 consumer 落库时从商品表权威反查，客户端值一律不进入
// 计费 / 打款链路。
type AsyncOrderTask struct {
	Ticket    string `json:"ticket"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Num       uint   `json:"num"`
	AddressID uint   `json:"address_id"`
	// SeckillID 非 0 表示秒杀单：单价 / 卖家取活动表，建单同事务扣活动 sold。
	SeckillID uint `json:"seckill_id,omitempty"`
}

// OrderTicketStatus 写到 Redis ticket key 的状态
type OrderTicketStatus struct {
	Status   string `json:"status"`
	OrderNum uint64 `json:"order_num,omitempty"`
	OrderID  uint   `json:"order_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// AsyncOrderProducer 投递抽象，便于测试用内存 channel 替换 RMQ
type AsyncOrderProducer interface {
	Publish(ctx context.Context, payload []byte) error
}

// AsyncOrderTicketStore ticket 状态存储抽象，默认走 Redis
type AsyncOrderTicketStore interface {
	Put(ctx context.Context, ticket string, st OrderTicketStatus, ttl time.Duration) error
	Get(ctx context.Context, ticket string) (OrderTicketStatus, bool, error)
}

// rmqProducer 默认实现：投到 order.create.async 交换机
type rmqProducer struct{}

func (rmqProducer) Publish(ctx context.Context, payload []byte) error {
	return rabbitmq.PublishOrderAsync(ctx, payload)
}

// redisTicketStore 默认实现
type redisTicketStore struct{}

func (redisTicketStore) Put(ctx context.Context, ticket string, st OrderTicketStatus, ttl time.Duration) error {
	body, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return cache.RedisClient.Set(ctx, OrderTicketKey(ticket), body, ttl).Err()
}

func (redisTicketStore) Get(ctx context.Context, ticket string) (OrderTicketStatus, bool, error) {
	raw, err := cache.RedisClient.Get(ctx, OrderTicketKey(ticket)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return OrderTicketStatus{}, false, nil
		}
		return OrderTicketStatus{}, false, err
	}
	var st OrderTicketStatus
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return OrderTicketStatus{}, false, err
	}
	return st, true, nil
}

var (
	defaultAsyncProducer AsyncOrderProducer    = rmqProducer{}
	defaultTicketStore   AsyncOrderTicketStore = redisTicketStore{}
)

// SetAsyncOrderProducer 注入自定义 producer（测试或灰度切换用）
func SetAsyncOrderProducer(p AsyncOrderProducer) { defaultAsyncProducer = p }

// SetAsyncOrderTicketStore 注入自定义 ticket store
func SetAsyncOrderTicketStore(s AsyncOrderTicketStore) { defaultTicketStore = s }

// AsyncOrderProducerInstance 暴露给 consumer 写回结果（共享 store）
func AsyncOrderTicketStoreInstance() AsyncOrderTicketStore { return defaultTicketStore }

// OrderEnqueueResp enqueue 接口响应
type OrderEnqueueResp struct {
	Ticket string `json:"ticket"`
	Status string `json:"status"`
}

// OrderEnqueue 异步下单：reserve 库存 + 写 ticket + 投 MQ
func (s *OrderSrv) OrderEnqueue(ctx context.Context, req *OrderCreateReq) (OrderEnqueueResp, error) {
	return s.enqueue(ctx, req, 0, NewTicket())
}

// NewTicket 生成异步下单 ticket（snowflake，可解析出生成时刻）。
func NewTicket() string { return fmt.Sprintf("%d", snowflake.GenSnowflakeID()) }

// SeckillEnqueue 秒杀单走同一条异步链路，ticket 由调用方先生成并记进秒杀在途集合；
// 返回 error 时按任务没投出去处理（预占已在内部释放），由调用方按 ticket 归还名额。
func (s *OrderSrv) SeckillEnqueue(ctx context.Context, req *OrderCreateReq, seckillID uint, ticket string) (OrderEnqueueResp, error) {
	return s.enqueue(ctx, req, seckillID, ticket)
}

// AsyncTaskMaxAge 任务在 MQ 里排队超过这个时长，consumer 不再建单、按失败补偿：
// 比 ticket TTL 短，ticket 过期后对账按「没人会再处理」回收在途标记时，不会和 consumer 抢着处理同一张单。
const AsyncTaskMaxAge = OrderTicketTTL - 10*time.Minute

// markAsyncInflight / unmarkAsyncInflight 维护商品的排队中预占（见 cache.OrderAsyncInflightKey）。
// 失败只记日志：标记多了只会让预占对账少回收，少了退化为原来的两次采样。
func markAsyncInflight(ctx context.Context, productID uint, ticket string, num uint) {
	if err := cache.RedisClient.ZAdd(ctx, cache.OrderAsyncInflightKey(productID), redis.Z{
		Score: float64(time.Now().Unix()), Member: asyncInflightMember(ticket, num),
	}).Err(); err != nil {
		util.LogrusObj.Errorf("mark async inflight failed ticket=%s err=%v", ticket, err)
	}
}

func unmarkAsyncInflight(ctx context.Context, productID uint, ticket string, num uint) {
	if err := cache.RedisClient.ZRem(ctx, cache.OrderAsyncInflightKey(productID), asyncInflightMember(ticket, num)).Err(); err != nil {
		util.LogrusObj.Errorf("unmark async inflight failed ticket=%s err=%v", ticket, err)
	}
}

func asyncInflightMember(ticket string, num uint) string { return fmt.Sprintf("%s:%d", ticket, num) }

// asyncInflightNum 商品排队中的预占件数；顺手清掉超过 ticket TTL 的标记（崩溃留下的，consumer 已不会再处理）。
func asyncInflightNum(ctx context.Context, productID uint) (int64, error) {
	key := cache.OrderAsyncInflightKey(productID)
	minScore := fmt.Sprintf("%d", time.Now().Add(-OrderTicketTTL).Unix())
	if err := cache.RedisClient.ZRemRangeByScore(ctx, key, "-inf", "("+minScore).Err(); err != nil {
		return 0, err
	}
	members, err := cache.RedisClient.ZRange(ctx, key, 0, -1).Result()
	if err != nil {
		return 0, err
	}
	var sum int64
	for _, m := range members {
		var n int64
		if i := strings.LastIndexByte(m, ':'); i >= 0 {
			n, _ = strconv.ParseInt(m[i+1:], 10, 64)
		}
		sum += n
	}
	return sum, nil
}

func (s *OrderSrv) enqueue(ctx context.Context, req *OrderCreateReq, seckillID uint, ticket string) (OrderEnqueueResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return OrderEnqueueResp{}, err
	}
	if err = ensureUnpaidQuota(ctx, u.Id); err != nil {
		return OrderEnqueueResp{}, err
	}

	// 地址归属在入队前同步校验：失败快速拒绝，避免白白预扣库存 / 投递无效任务。
	if err = address.NewAddressDao(ctx).EnsureOwned(req.AddressID, u.Id); err != nil {
		util.LogrusObj.Errorf("async enqueue address ownership check failed addr=%d user=%d err=%v", req.AddressID, u.Id, err)
		return OrderEnqueueResp{}, err
	}

	if err = product.ReserveStock(ctx, req.ProductID, int64(req.Num)); err != nil {
		util.LogrusObj.Errorf("async enqueue reserve stock failed product=%d num=%d err=%v", req.ProductID, req.Num, err)
		return OrderEnqueueResp{}, err
	}
	markAsyncInflight(ctx, req.ProductID, ticket, req.Num)
	// 投递没成功：先释放预占再去标记（中间被对账看到只会少回收，不会多回收）
	undo := func(what string) {
		if relErr := cache.ReleaseReservation(ctx, req.ProductID, int64(req.Num)); relErr != nil {
			util.LogrusObj.Errorf("release reservation on %s failure failed: %v", what, relErr)
		}
		unmarkAsyncInflight(ctx, req.ProductID, ticket, req.Num)
	}

	if err = defaultTicketStore.Put(ctx, ticket, OrderTicketStatus{Status: OrderTicketStatusPending}, OrderTicketTTL); err != nil {
		util.LogrusObj.Errorf("async enqueue write ticket failed: %v", err)
		undo("ticket write")
		return OrderEnqueueResp{}, err
	}

	task := AsyncOrderTask{
		Ticket:    ticket,
		UserID:    u.Id,
		ProductID: req.ProductID,
		Num:       req.Num,
		AddressID: req.AddressID,
		SeckillID: seckillID,
	}
	body, err := json.Marshal(task)
	if err != nil {
		util.LogrusObj.Errorf("async enqueue marshal failed: %v", err)
		undo("marshal")
		return OrderEnqueueResp{}, err
	}

	if err = defaultAsyncProducer.Publish(ctx, body); err != nil {
		util.LogrusObj.Errorf("async enqueue publish failed: %v", err)
		undo("publish")
		_ = defaultTicketStore.Put(ctx, ticket, OrderTicketStatus{
			Status: OrderTicketStatusFailed,
			Reason: "publish failed",
		}, OrderTicketTTL)
		return OrderEnqueueResp{}, err
	}

	return OrderEnqueueResp{Ticket: ticket, Status: OrderTicketStatusPending}, nil
}

// OrderStatus 读 ticket 状态
func (s *OrderSrv) OrderStatus(ctx context.Context, ticket string) (OrderTicketStatus, error) {
	if ticket == "" {
		return OrderTicketStatus{}, errors.New("ticket 不能为空")
	}
	st, ok, err := defaultTicketStore.Get(ctx, ticket)
	if err != nil {
		util.LogrusObj.Errorf("order status read ticket failed: %v", err)
		return OrderTicketStatus{}, err
	}
	if !ok {
		return OrderTicketStatus{}, errors.New("ticket 不存在或已过期")
	}
	return st, nil
}
