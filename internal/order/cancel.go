package order

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
)

// CancelUnpaidOrder 关闭未支付订单 (幂等)。
//  0. 还有能付款的外部支付（Stripe 会话 / Xcash 账单 / Web3 签名占位）就先不关，等它们过期后由下一轮 cron 关
//  1. CloseOrderWithCheck: 只对 UnPaid 状态的普通订单生效（已付定金的预售单除外），二次调用直接 no-op
//  2. 不再回写 product.Num：未支付订单从未真正扣减过 DB 库存，回写会虚高
//  3. 释放 Redis reserved 预占 (退回 available)；秒杀单同事务归还活动 sold，提交后归还 Redis 秒杀名额
//  4. outbox 写 order.cancelled 事件
func CancelUnpaidOrder(orderNum uint64) error {
	ctx := context.Background()
	baseDao := NewOrderDao(ctx)
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return nil
	}
	if order.Type == consts.OrderWaitPay && order.PreorderStage == 0 {
		// 用户可能正在外部渠道付款：现在关单，钱到账时订单已关闭，只能进异常单人工退款。
		active, err := hasActivePaymentIntent(ctx, order.ID)
		if err != nil {
			return err
		}
		if active {
			util.LogrusObj.Infof("订单有在途的外部支付，推迟关单 orderNum=%d", orderNum)
			return nil
		}
	}

	var closed bool
	err = baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := NewOrderDaoByDB(tx).CloseOrderWithCheck(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		closed = true
		if order.SeckillID != 0 && seckillBinder != nil {
			if err := seckillBinder.UnbindTx(tx, order); err != nil {
				return err
			}
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderCancelled", "order.cancelled", order.ID,
			events.OrderCancelled{
				OrderID:            order.ID,
				OrderNum:           orderNum,
				UserID:             order.UserID,
				ProductID:          order.ProductID,
				Num:                order.Num,
				Reason:             "timeout",
				PromoRuleID:        order.PromoRuleID,
				PromoDiscountCents: order.PromoDiscountCents,
			},
		)
	})
	if err != nil {
		return err
	}
	if !closed {
		return nil
	}

	if relErr := cache.ReleaseReservation(ctx, order.ProductID, int64(order.Num)); relErr != nil {
		util.LogrusObj.Errorf("release reservation on cancel failed orderNum=%d err=%v", orderNum, relErr)
	}
	if order.SeckillID != 0 && seckillBinder != nil {
		seckillBinder.Return(ctx, order)
	}

	// 满减预算退还不在这里同步执行：order.cancelled 事件已携带 promo_rule_id /
	// promo_discount_cents，由 promo 侧消费该事件异步完成（at-least-once + 幂等台账）。
	return nil
}

// hasActivePaymentIntent 订单是否还有能付款的外部支付：Stripe 会话、Web3 签名占位（Redis 键随有效期过期），
// 以及 Xcash 账单（等待付款且未过期，或已付款在等风控结论）。
// 表名和状态值直接写（即 payment.XcashPaymentIntent / XcashIntentWaiting / XcashIntentRiskPending）：
// payment 依赖 order，这里不能反向 import。
func hasActivePaymentIntent(ctx context.Context, orderID uint) (bool, error) {
	n, err := cache.RedisClient.Exists(ctx, cache.StripeCheckoutKey(orderID), cache.Web3PendingKey(orderID)).Result()
	if err != nil || n > 0 {
		return n > 0, err
	}
	var intents int64
	err = NewOrderDao(ctx).Table("xcash_payment_intent").
		Where("order_id = ? AND (status = ? OR (status = ? AND expires_at > ?))", orderID, "risk_pending", "waiting", time.Now()).
		Count(&intents).Error
	return intents > 0, err
}
