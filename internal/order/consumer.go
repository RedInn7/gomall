package order

import (
	"context"
	"encoding/json"
	"errors"
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
type asyncOrderWriter func(ctx context.Context, task AsyncOrderTask, order *Order, quote *orderQuote) error

var defaultAsyncOrderWriter asyncOrderWriter = persistAsyncOrder

// SetAsyncOrderWriter 注入自定义写订单实现（仅测试用），传 nil 恢复默认实现。
// 优惠方案是包内类型，包外替身拿不到，只看 task / order。
func SetAsyncOrderWriter(w func(ctx context.Context, task AsyncOrderTask, order *Order) error) {
	if w == nil {
		defaultAsyncOrderWriter = persistAsyncOrder
		return
	}
	defaultAsyncOrderWriter = func(ctx context.Context, task AsyncOrderTask, order *Order, _ *orderQuote) error {
		return w(ctx, task, order)
	}
}

// persistAsyncOrder 与同步下单同一套事务：建单 + 扣满减预算 + 核销券 + outbox，
// 同一件商品走排队下单和直接下单拿到的优惠一致。
func persistAsyncOrder(ctx context.Context, task AsyncOrderTask, order *Order, quote *orderQuote) error {
	return dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		if e := NewOrderDaoByDB(tx).CreateOrder(order); e != nil {
			return e
		}
		if order.SeckillID != 0 {
			if seckillBinder == nil {
				return errSeckillNotRegistered
			}
			if e := seckillBinder.BindTx(tx, order); e != nil {
				return e
			}
		}
		if e := GetOrderSrv().applyDiscountsInTx(tx, order, quote); e != nil {
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
// 幂等：订单按 ticket 唯一。MQ 重投（上次建完单没来得及 Ack）时找到已建的单直接回写成功，
// 不再新建一张共用同一份预占的单，也不释放那张单的预占。
//
// 失败语义：返回 error 时调用方应让 RMQ Nack 不重投，库存已在内部 release。
func HandleAsyncOrderTask(ctx context.Context, body []byte) error {
	var task AsyncOrderTask
	if err := json.Unmarshal(body, &task); err != nil {
		util.LogrusObj.Errorf("async order task unmarshal failed: %v", err)
		return err
	}

	existing, err := NewOrderDao(ctx).GetOrderByTicket(task.Ticket)
	if err != nil {
		// 查不清是否已建过单就不动预占：没建的话由库存对账回收。
		util.LogrusObj.Errorf("async order lookup ticket failed ticket=%s err=%v", task.Ticket, err)
		return err
	}
	if existing != nil {
		finishAsyncTask(ctx, task, existing)
		return nil
	}

	// 排队太久：ticket 快过期，对账随后会按「没人处理」回收在途标记；这里先按失败收尾，两边不会都处理同一张单。
	if at, err := snowflake.TimeOf(task.Ticket); err == nil && time.Since(at) > AsyncTaskMaxAge {
		staleErr := errors.New("排队超时，请重新下单")
		util.LogrusObj.Errorf("async order task stale ticket=%s age=%v", task.Ticket, time.Since(at))
		abortAsyncTask(ctx, task, staleErr)
		return staleErr
	}

	// 单价与卖家都以商品表（秒杀单以活动表）为准，不信投递消息里的金额 / boss——异步链路同样
	// 不能把定价权与收款方交给客户端。反查失败、要用的券已不可用，都属于不可落单的硬错误：
	// 释放预扣库存、写失败 ticket，与下方写库失败同路处理。
	unitCents, bossID, quote, settleErr := resolveTaskSettlement(ctx, task)
	if settleErr != nil {
		util.LogrusObj.Errorf("async order resolve settlement failed ticket=%s product=%d err=%v",
			task.Ticket, task.ProductID, settleErr)
		abortAsyncTask(ctx, task, settleErr)
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
		Ticket:    &task.Ticket,
		SeckillID: task.SeckillID,
		// 减免字段在事务内按扣预算 / 核销券的实际结果写回
		FinalCents: quote.subtotal,
	}

	err = defaultAsyncOrderWriter(ctx, task, order, quote)
	if err != nil {
		// 并发重投撞上 ticket 唯一索引 / 提交成功但确认丢了：单已经在，按成功处理。
		existing, lookErr := NewOrderDao(ctx).GetOrderByTicket(task.Ticket)
		if lookErr != nil {
			// 查不清单在不在就不补偿：补偿了而单其实已提交，会把还占着的预占 / 秒杀名额放出去超卖。
			// 预占和在途标记留给对账（按 ticket 查 DB 决定转正还是归还）。
			util.LogrusObj.Errorf("async order recheck ticket failed ticket=%s txErr=%v err=%v", task.Ticket, err, lookErr)
			return err
		}
		if existing != nil {
			finishAsyncTask(ctx, task, existing)
			return nil
		}
		util.LogrusObj.Errorf("async order task tx failed ticket=%s err=%v", task.Ticket, err)
		abortAsyncTask(ctx, task, err)
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

	finishAsyncTask(ctx, task, order)
	return nil
}

// finishAsyncTask 单已落库：去掉排队标记、秒杀 ticket 转正、回写 ticket 成功。
func finishAsyncTask(ctx context.Context, task AsyncOrderTask, o *Order) {
	unmarkAsyncInflight(ctx, task.ProductID, task.Ticket, task.Num)
	if task.SeckillID != 0 && seckillBinder != nil {
		seckillBinder.Settle(ctx, task.SeckillID, task.UserID, task.Ticket)
	}
	putAsyncTicketOK(ctx, task.Ticket, o)
}

// putAsyncTicketOK 回写 ticket 成功态；写失败只记日志，订单已落库。
func putAsyncTicketOK(ctx context.Context, ticket string, o *Order) {
	if err := defaultTicketStore.Put(ctx, ticket, OrderTicketStatus{
		Status:   OrderTicketStatusOK,
		OrderNum: o.OrderNum,
		OrderID:  o.ID,
	}, OrderTicketTTL); err != nil {
		util.LogrusObj.Errorf("async order task write ticket ok failed ticket=%s err=%v", ticket, err)
	}
}

// resolveTaskSettlement 秒杀单取活动价与活动卖家，普通单取商品表并试算满减 / 校验券。
func resolveTaskSettlement(ctx context.Context, task AsyncOrderTask) (int64, uint, *orderQuote, error) {
	if task.SeckillID == 0 {
		cents, categoryID, bossID, err := resolveProductPricing(ctx, task.ProductID)
		if err != nil {
			return 0, 0, nil, err
		}
		q, err := GetOrderSrv().quoteOrder(ctx, task.UserID, task.ProductID, categoryID,
			cents, int64(task.Num), task.UserCouponID)
		return cents, bossID, q, err
	}
	if seckillBinder == nil {
		return 0, 0, nil, errSeckillNotRegistered
	}
	if task.UserCouponID != 0 {
		return 0, 0, nil, errors.New("秒杀单不能用券")
	}
	// 秒杀价即成交价，不叠满减 / 券。
	cents, bossID, err := seckillBinder.Price(ctx, task.SeckillID, task.ProductID)
	if err != nil {
		return 0, 0, nil, err
	}
	return cents, bossID, &orderQuote{subtotal: cents * int64(task.Num)}, nil
}

// abortAsyncTask 任务确定落不了单：释放商品预占、归还秒杀名额、写失败 ticket。
func abortAsyncTask(ctx context.Context, task AsyncOrderTask, cause error) {
	if relErr := cache.ReleaseReservation(ctx, task.ProductID, int64(task.Num)); relErr != nil {
		util.LogrusObj.Errorf("release reservation on async order failure failed ticket=%s err=%v", task.Ticket, relErr)
	}
	unmarkAsyncInflight(ctx, task.ProductID, task.Ticket, task.Num)
	if task.SeckillID != 0 && seckillBinder != nil {
		seckillBinder.Abort(ctx, task.SeckillID, task.UserID, task.Ticket, cause)
	}
	_ = defaultTicketStore.Put(ctx, task.Ticket, OrderTicketStatus{
		Status: OrderTicketStatusFailed,
		Reason: cause.Error(),
	}, OrderTicketTTL)
}
