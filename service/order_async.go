package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/types"
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

// AsyncOrderTask 投递到 MQ 的消息体
type AsyncOrderTask struct {
	Ticket    string `json:"ticket"`
	UserID    uint   `json:"user_id"`
	ProductID uint   `json:"product_id"`
	Num       uint   `json:"num"`
	Money     int    `json:"money"`
	AddressID uint   `json:"address_id"`
	BossID    uint   `json:"boss_id"`
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
func (s *OrderSrv) OrderEnqueue(ctx context.Context, req *types.OrderCreateReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return nil, err
	}

	if err = cache.ReserveStock(ctx, req.ProductID, int64(req.Num)); err != nil {
		util.LogrusObj.Errorf("async enqueue reserve stock failed product=%d num=%d err=%v", req.ProductID, req.Num, err)
		return nil, err
	}

	ticket := fmt.Sprintf("%d", snowflake.GenSnowflakeID())

	if err = defaultTicketStore.Put(ctx, ticket, OrderTicketStatus{Status: OrderTicketStatusPending}, OrderTicketTTL); err != nil {
		util.LogrusObj.Errorf("async enqueue write ticket failed: %v", err)
		if relErr := cache.ReleaseReservation(ctx, req.ProductID, int64(req.Num)); relErr != nil {
			util.LogrusObj.Errorf("release reservation on ticket write failure failed: %v", relErr)
		}
		return nil, err
	}

	task := AsyncOrderTask{
		Ticket:    ticket,
		UserID:    u.Id,
		ProductID: req.ProductID,
		Num:       req.Num,
		Money:     req.Money,
		AddressID: req.AddressID,
		BossID:    req.BossID,
	}
	body, err := json.Marshal(task)
	if err != nil {
		util.LogrusObj.Errorf("async enqueue marshal failed: %v", err)
		if relErr := cache.ReleaseReservation(ctx, req.ProductID, int64(req.Num)); relErr != nil {
			util.LogrusObj.Errorf("release reservation on marshal failure failed: %v", relErr)
		}
		return nil, err
	}

	if err = defaultAsyncProducer.Publish(ctx, body); err != nil {
		util.LogrusObj.Errorf("async enqueue publish failed: %v", err)
		if relErr := cache.ReleaseReservation(ctx, req.ProductID, int64(req.Num)); relErr != nil {
			util.LogrusObj.Errorf("release reservation on publish failure failed: %v", relErr)
		}
		_ = defaultTicketStore.Put(ctx, ticket, OrderTicketStatus{
			Status: OrderTicketStatusFailed,
			Reason: "publish failed",
		}, OrderTicketTTL)
		return nil, err
	}

	return OrderEnqueueResp{Ticket: ticket, Status: OrderTicketStatusPending}, nil
}

// OrderStatus 读 ticket 状态
func (s *OrderSrv) OrderStatus(ctx context.Context, ticket string) (resp interface{}, err error) {
	if ticket == "" {
		return nil, errors.New("ticket 不能为空")
	}
	st, ok, err := defaultTicketStore.Get(ctx, ticket)
	if err != nil {
		util.LogrusObj.Errorf("order status read ticket failed: %v", err)
		return nil, err
	}
	if !ok {
		return nil, errors.New("ticket 不存在或已过期")
	}
	return st, nil
}
