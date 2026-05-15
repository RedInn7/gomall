package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// CancelUnpaidOrder 真正执行关单 + 库存回滚，幂等。
// 已支付 / 已取消 的订单会被 CloseOrderWithCheck 直接跳过。
func CancelUnpaidOrder(orderNum uint64) error {
	baseDao := dao.NewOrderDao(context.Background())
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return nil
	}
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		txOrderDao := dao.NewOrderDaoByDB(tx)
		txProductDao := dao.NewProductDaoWithDB(tx)
		ok, err := txOrderDao.CloseOrderWithCheck(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		ok, err = txProductDao.RollbackStock(order.ProductID, order.Num)
		if err != nil {
			return err
		}
		if !ok {
			util.LogrusObj.Warnf("回滚库存失败 orderNum=%d", orderNum)
			return errors.New("回滚库存失败")
		}
		return nil
	})
}
