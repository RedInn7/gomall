package service

import (
	"context"

	"gorm.io/gorm"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

// CancelUnpaidOrder 关闭未支付订单 (幂等)。
//   1. CloseOrderWithCheck: 只对 UnPaid 状态的订单生效，二次调用直接 no-op
//   2. 不再回写 product.Num：未支付订单从未真正扣减过 DB 库存，回写会虚高
//   3. 释放 Redis reserved 预占 (退回 available)
//   4. outbox 写 order.cancelled 事件
func CancelUnpaidOrder(orderNum uint64) error {
	ctx := context.Background()
	baseDao := dao.NewOrderDao(ctx)
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return nil
	}

	var closed bool
	err = baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := dao.NewOrderDaoByDB(tx).CloseOrderWithCheck(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		closed = true
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderCancelled", "order.cancelled", order.ID,
			events.OrderCancelled{
				OrderID:   order.ID,
				OrderNum:  orderNum,
				UserID:    order.UserID,
				ProductID: order.ProductID,
				Num:       order.Num,
				Reason:    "timeout",
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
	return nil
}
