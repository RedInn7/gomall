package order

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/rabbitmq"
	"github.com/RedInn7/gomall/service/events"
)

// asyncOrderWriter 真正写订单和 outbox 的抽象，测试时替换为内存实现
type asyncOrderWriter func(ctx context.Context, task AsyncOrderTask, order *Order) error

var defaultAsyncOrderWriter asyncOrderWriter = persistAsyncOrder

// SetAsyncOrderWriter 注入自定义写订单实现（仅测试用）
func SetAsyncOrderWriter(w asyncOrderWriter) { defaultAsyncOrderWriter = w }

func persistAsyncOrder(ctx context.Context, task AsyncOrderTask, order *Order) error {
	return dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		if e := NewOrderDaoByDB(tx).CreateOrder(order); e != nil {
			return e
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderCreated", "order.created", order.ID,
			events.OrderCreated{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    task.UserID,
				ProductID: task.ProductID,
				Num:       int(task.Num),
			},
		)
	})
}

// HandleAsyncOrderTask 把一条 enqueue 投递的任务落 DB（含 outbox 事件），并写回 ticket 结果
// 调用方负责并发模型（RMQ goroutine / 内存 channel 都行），本函数只关心单条任务的语义。
//
// 失败语义：返回 error 时调用方应让 RMQ Nack 不重投，库存已在内部 release。
func HandleAsyncOrderTask(ctx context.Context, body []byte) error {
	var task AsyncOrderTask
	if err := json.Unmarshal(body, &task); err != nil {
		util.LogrusObj.Errorf("async order task unmarshal failed: %v", err)
		return err
	}

	// 单价与卖家都以商品表为准，不信投递消息里的金额 / boss——异步链路同样不能把定价权与
	// 收款方交给客户端。反查失败属于不可落单的硬错误：释放预扣库存、写失败 ticket，与下方
	// 写库失败同路处理。
	unitCents, bossID, settleErr := resolveProductSettlement(ctx, task.ProductID)
	if settleErr != nil {
		util.LogrusObj.Errorf("async order resolve settlement failed ticket=%s product=%d err=%v",
			task.Ticket, task.ProductID, settleErr)
		if relErr := cache.ReleaseReservation(ctx, task.ProductID, int64(task.Num)); relErr != nil {
			util.LogrusObj.Errorf("release reservation on settlement resolve failure failed: %v", relErr)
		}
		_ = defaultTicketStore.Put(ctx, task.Ticket, OrderTicketStatus{
			Status: OrderTicketStatusFailed,
			Reason: settleErr.Error(),
		}, OrderTicketTTL)
		return settleErr
	}

	order := &Order{
		UserID:    task.UserID,
		ProductID: task.ProductID,
		BossID:    bossID, // 卖家以商品表为准，忽略 task.BossID
		Num:       int(task.Num),
		Money:     unitCents,
		Type:      consts.OrderWaitPay,
		AddressID: task.AddressID,
		OrderNum:  uint64(snowflake.GenSnowflakeID()),
	}

	err := defaultAsyncOrderWriter(ctx, task, order)
	if err != nil {
		util.LogrusObj.Errorf("async order task tx failed ticket=%s err=%v", task.Ticket, err)
		if relErr := cache.ReleaseReservation(ctx, task.ProductID, int64(task.Num)); relErr != nil {
			util.LogrusObj.Errorf("release reservation on async tx failure failed: %v", relErr)
		}
		_ = defaultTicketStore.Put(ctx, task.Ticket, OrderTicketStatus{
			Status: OrderTicketStatusFailed,
			Reason: err.Error(),
		}, OrderTicketTTL)
		return err
	}

	data := redis.Z{
		Score:  float64(time.Now().Unix()) + 15*time.Minute.Seconds(),
		Member: order.OrderNum,
	}
	cache.RedisClient.ZAdd(cache.RedisContext, OrderTimeKey, data)

	if rabbitmq.GlobalRabbitMQ != nil {
		if pubErr := rabbitmq.PublishOrderCancelDelay(ctx, order.OrderNum, rabbitmq.OrderCancelDelay); pubErr != nil {
			util.LogrusObj.Errorf("publish delay cancel failed orderNum=%d err=%v", order.OrderNum, pubErr)
		}
	}

	if err := defaultTicketStore.Put(ctx, task.Ticket, OrderTicketStatus{
		Status:   OrderTicketStatusOK,
		OrderNum: order.OrderNum,
		OrderID:  order.ID,
	}, OrderTicketTTL); err != nil {
		util.LogrusObj.Errorf("async order task write ticket ok failed ticket=%s err=%v", task.Ticket, err)
	}
	return nil
}
