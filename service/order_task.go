package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

// autoConfirmReceiveDays 已发货订单超过这个天数仍未确认收货，cron 兜底自动确认。
// 默认 7 天，与主流电商口径一致；商家自定义在 routes/admin 后台落地后切换。
const autoConfirmReceiveDays = 7

type OrderTaskService struct {
}

func (s *OrderTaskService) RunOrderTimeoutCheck() {
	baseDao := dao.NewOrderDao(context.Background())
	orders, err := baseDao.GetTimeoutOrders(15, 100)
	if err != nil {
		util.LogrusObj.Errorf("Cron Job Error: fetch orders failed: %v\n", err)
		return
	}

	for _, order := range orders {
		err := baseDao.DB.Transaction(func(tx *gorm.DB) error {
			txOrderDao := dao.NewOrderDaoByDB(tx)
			txProductDao := dao.NewProductDaoWithDB(tx)
			success, err := txOrderDao.CloseOrderWithCheck(order.OrderNum)
			if err != nil {
				return err
			}
			if !success {
				return errors.New("关单失败")
			}
			success, err = txProductDao.RollbackStock(order.ProductID, order.Num)
			if err != nil {
				return err
			}
			if !success {
				return errors.New("回滚失败")
			}
			return nil
		})
		if err != nil {
			util.LogrusObj.Errorf("关单失败，orderNum:%v,err:%v\n", order.OrderNum, err)
			continue
		}
		util.LogrusObj.Infof("orderNum:%v关单成功", order.OrderNum)
	}
}

// RunAutoConfirmReceive 对长时间未确认收货的订单兜底自动 Completed。
// 行业惯例 7 天，gomall 默认沿用。一次拉一批，单批失败不影响其它订单。
// 同事务推进状态机 + 写 order.completed 事件，下游服务（点评 / 结算 / 数据）由事件驱动。
func (s *OrderTaskService) RunAutoConfirmReceive() {
	ctx := context.Background()
	baseDao := dao.NewOrderDao(ctx)
	orders, err := baseDao.GetTimeoutWaitReceive(autoConfirmReceiveDays, 100)
	if err != nil {
		util.LogrusObj.Errorf("Cron Job Error: fetch wait-receive orders failed: %v", err)
		return
	}
	for _, order := range orders {
		err := baseDao.DB.Transaction(func(tx *gorm.DB) error {
			ok, err := dao.NewOrderDaoByDB(tx).ConfirmReceive(order.OrderNum)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("自动确认收货失败：订单状态已变更")
			}
			return dao.NewOutboxDaoByDB(tx).Insert(
				"order", "OrderCompleted", "order.completed", order.ID,
				events.OrderCompletedEvent{
					OrderID:  order.ID,
					OrderNum: order.OrderNum,
					UserID:   order.UserID,
					Auto:     true,
				},
			)
		})
		if err != nil {
			util.LogrusObj.Errorf("自动确认收货失败 orderNum=%v err=%v", order.OrderNum, err)
			continue
		}
		util.LogrusObj.Infof("orderNum:%v 自动确认收货成功", order.OrderNum)
	}
}
