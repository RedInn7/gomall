package service

import (
	"context"
	"errors"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"gorm.io/gorm"
)

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
			util.LogrusObj.Errorf("关单失败，orderNum:%v\n", order.OrderNum)
			continue
		}
		util.LogrusObj.Infof("orderNum:%v关单成功", order.OrderNum)
	}
}
