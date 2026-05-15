package service

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/service/events"
	"github.com/RedInn7/gomall/types"
)

var PaymentSrvIns *PaymentSrv
var PaymentSrvOnce sync.Once

type PaymentSrv struct {
}

func GetPaymentSrv() *PaymentSrv {
	PaymentSrvOnce.Do(func() {
		PaymentSrvIns = &PaymentSrv{}
	})
	return PaymentSrvIns
}

// PayDown 支付操作。BossID/ProductID/Num/Money 全部从订单取，不读 req。
func (s *PaymentSrv) PayDown(ctx context.Context, req *types.PaymentDownReq) (resp interface{}, err error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	if len(req.Key) != consts.EncryptMoneyKeyLength {
		err = errors.New("支付密码长度错误")
		log.LogrusObj.Error(err)
		return nil, err
	}

	var (
		paidProductID uint
		paidNum       int
	)
	err = dao.NewOrderDao(ctx).Transaction(func(tx *gorm.DB) error {
		uId := u.Id

		order, err := dao.NewOrderDaoByDB(tx).GetOrderById(req.OrderId, uId)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		if order.Type != consts.UnPaid {
			err = errors.New("订单状态非未支付，无法重复支付")
			log.LogrusObj.Error(err)
			return err
		}

		bossID := order.BossID
		productID := order.ProductID
		num := order.Num
		paidProductID = productID
		paidNum = num
		totalMoney := order.Money * int64(num)

		userDao := dao.NewUserDaoByDB(tx)
		user, err := userDao.GetUserById(uId)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		userMoney, err := user.DecryptMoney(req.Key)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if userMoney-totalMoney < 0 {
			log.LogrusObj.Error("金额不足")
			return errors.New("金额不足")
		}

		user.Money = strconv.FormatInt(userMoney-totalMoney, 10)
		user.Money, err = user.EncryptMoney(req.Key)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		err = userDao.UpdateUserById(uId, user)
		if err != nil { // 更新用户金额失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		boss, err := userDao.GetUserById(bossID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		bossMoney, err := boss.DecryptMoney(req.Key)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		boss.Money = strconv.FormatInt(bossMoney+totalMoney, 10)
		boss.Money, err = boss.EncryptMoney(req.Key)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		err = userDao.UpdateUserById(bossID, boss)
		if err != nil { // 更新boss金额失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		productDao := dao.NewProductDaoByDB(tx)
		product, err := productDao.GetProductById(productID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if product.Num-num < 0 {
			log.LogrusObj.Error("存在超卖问题")
			return errors.New("存在超卖问题")
		}
		product.Num -= num
		err = productDao.UpdateProduct(productID, product)
		if err != nil { // 更新商品数量减少失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		// 更新订单状态
		order.Type = consts.OrderTypePendingShipping
		err = dao.NewOrderDaoByDB(tx).UpdateOrderById(req.OrderId, uId, order)
		if err != nil { // 更新订单失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		productUser := model.Product{
			Name:          product.Name,
			CategoryID:    product.CategoryID,
			Title:         product.Title,
			Info:          product.Info,
			ImgPath:       product.ImgPath,
			Price:         product.Price,
			DiscountPrice: product.DiscountPrice,
			Num:           num,
			OnSale:        false,
			BossID:        uId,
			BossName:      user.UserName,
			BossAvatar:    user.Avatar,
		}

		err = productDao.CreateProduct(&productUser)
		if err != nil { // 买完商品后创建成了自己的商品失败。订单失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		// outbox 事件：order.paid，事件投递交给 publisher 异步处理
		return dao.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderPaid", "order.paid", order.ID,
			events.OrderPaid{
				OrderID:   order.ID,
				OrderNum:  order.OrderNum,
				UserID:    uId,
				ProductID: productID,
				Num:       num,
			},
		)
	})

	if err != nil {
		log.LogrusObj.Error(err)
		return
	}

	// TX 已经把 product.Num 真正扣减了；同步把 Redis reserved 桶减掉
	if paidProductID > 0 && paidNum > 0 {
		if cErr := cache.CommitReservation(ctx, paidProductID, int64(paidNum)); cErr != nil {
			log.LogrusObj.Errorf("commit reservation failed product=%d num=%d err=%v", paidProductID, paidNum, cErr)
		}
	}

	return
}
