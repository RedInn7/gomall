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
	RefundSrvIns  *RefundSrv
	RefundSrvOnce sync.Once
)

// RefundSrv 处理退款三态：发起 / 同意 / 驳回。
// 本期仅推进订单状态机并写 outbox 事件，真正的退款扣款由下游 wallet / 支付服务
// 消费 order.refunded 事件后落地（含 tx_id 回填）。
type RefundSrv struct{}

func GetRefundSrv() *RefundSrv {
	RefundSrvOnce.Do(func() {
		RefundSrvIns = &RefundSrv{}
	})
	return RefundSrvIns
}

// refundAllowedFrom 用户可以从这些状态发起退款。
// WaitPay 不在列表里（未付款的应当走 cancel 关单）。
var refundAllowedFrom = []uint{
	consts.OrderWaitShip,
	consts.OrderWaitReceive,
	consts.OrderCompleted,
}

// RequestRefund 用户发起退款申请。
//   - from 必须落在 refundAllowedFrom：WaitShip / WaitReceive / Completed
//   - 校验 ctx 中的 userID 与订单归属一致
//   - 通过 DAO 的 WHERE IN 一次拦截非法 from，避免读后写竞态
func (s *RefundSrv) RequestRefund(ctx context.Context, orderNum uint64, reason string) error {
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
	if !inUintSlice(order.Type, refundAllowedFrom) {
		return ErrInvalidOrderStateTransition
	}
	fromType := order.Type
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := dao.NewOrderDaoByDB(tx).RequestRefund(orderNum, refundAllowedFrom)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidOrderStateTransition
		}
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderRefunding", "order.refunding", order.ID,
			events.OrderRefunding{
				OrderID:  order.ID,
				OrderNum: order.OrderNum,
				UserID:   order.UserID,
				FromType: fromType,
				Reason:   reason,
			},
		)
	})
}

// ApproveRefund 运营同意退款。
//   - 仅允许 Refunding -> Refunded
//   - 写 outbox(order.refunded)，amount 取订单 Money * Num；tx_id 留空待下游回填
//
// 真正的资金回退在下游 wallet 服务消费事件时执行；本服务仅做状态推进。
func (s *RefundSrv) ApproveRefund(ctx context.Context, orderNum uint64) error {
	baseDao := dao.NewOrderDao(ctx)
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return errors.New("订单不存在")
	}
	if order.Type != consts.OrderRefunding {
		return ErrInvalidOrderStateTransition
	}
	amount := order.Money * int64(order.Num)
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := dao.NewOrderDaoByDB(tx).ApproveRefund(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidOrderStateTransition
		}
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderRefunded", "order.refunded", order.ID,
			events.OrderRefundedEvent{
				OrderID:  order.ID,
				OrderNum: order.OrderNum,
				UserID:   order.UserID,
				Amount:   amount,
				TxID:     "",
			},
		)
	})
}

// RejectRefund 运营驳回退款，订单回到 Completed。
//   - 仅允许 Refunding -> Completed
//   - 写 outbox(order.refund_rejected) 让客服 / 用户系统得知
func (s *RefundSrv) RejectRefund(ctx context.Context, orderNum uint64, reason string) error {
	baseDao := dao.NewOrderDao(ctx)
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return errors.New("订单不存在")
	}
	if order.Type != consts.OrderRefunding {
		return ErrInvalidOrderStateTransition
	}
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := dao.NewOrderDaoByDB(tx).RejectRefund(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return ErrInvalidOrderStateTransition
		}
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderRefundRejected", "order.refund_rejected", order.ID,
			events.OrderRefundRejected{
				OrderID:  order.ID,
				OrderNum: order.OrderNum,
				UserID:   order.UserID,
				Reason:   reason,
			},
		)
	})
}

func inUintSlice(v uint, s []uint) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
