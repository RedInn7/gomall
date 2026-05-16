package service

import (
	"context"
	"errors"
	"sync"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

var (
	ShippingSrvIns  *ShippingSrv
	ShippingSrvOnce sync.Once
)

// ShippingSrv 负责订单履约阶段的状态推进：发货 / 确认收货。
type ShippingSrv struct{}

func GetShippingSrv() *ShippingSrv {
	ShippingSrvOnce.Do(func() {
		ShippingSrvIns = &ShippingSrv{}
	})
	return ShippingSrvIns
}

// ShipOrder 商家发货。
//   - 仅允许从 WaitShip 推进到 WaitReceive；条件 UPDATE 兜底幂等
//   - tracking / carrier 仅经 outbox 事件透传，下游消费者负责持久化或对接物流系统
//   - 状态推进与 outbox 写入同事务，保证不会"已发货但下游收不到事件"
func (s *ShippingSrv) ShipOrder(ctx context.Context, orderNum uint64, trackingNo, carrier string) error {
	baseDao := dao.NewOrderDao(ctx)
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return errors.New("订单不存在")
	}
	if order.Type != consts.OrderWaitShip {
		return ErrInvalidOrderStateTransition
	}
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := dao.NewOrderDaoByDB(tx).ShipOrder(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidOrderStateTransition
		}
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderShipped", "order.shipped", order.ID,
			events.OrderShipped{
				OrderID:    order.ID,
				OrderNum:   order.OrderNum,
				UserID:     order.UserID,
				TrackingNo: trackingNo,
				Carrier:    carrier,
			},
		)
	})
}

// ConfirmReceive 用户确认收货。
//   - 仅允许从 WaitReceive 推进到 Completed
//   - 校验 ctx 中的 userID 与订单归属一致，避免越权
//   - 同事务写 outbox(order.completed)，Auto=false 表示用户主动确认
func (s *ShippingSrv) ConfirmReceive(ctx context.Context, orderNum uint64) error {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		util.LogrusObj.Error(err)
		return err
	}
	baseDao := dao.NewOrderDao(ctx)
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return errors.New("订单不存在")
	}
	if order.UserID != u.Id {
		return errors.New("无权操作该订单")
	}
	if order.Type != consts.OrderWaitReceive {
		return ErrInvalidOrderStateTransition
	}
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := dao.NewOrderDaoByDB(tx).ConfirmReceive(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidOrderStateTransition
		}
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderCompleted", "order.completed", order.ID,
			events.OrderCompletedEvent{
				OrderID:  order.ID,
				OrderNum: order.OrderNum,
				UserID:   order.UserID,
				Auto:     false,
			},
		)
	})
}
