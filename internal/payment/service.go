package payment

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
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
// 成功不回传数据，data 为 null。
func (s *PaymentSrv) PayDown(ctx context.Context, req *PaymentDownReq) (resp *PayDownResp, err error) {
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
	err = orderpkg.NewOrderDao(ctx).Transaction(func(tx *gorm.DB) error {
		uId := u.Id

		order, err := orderpkg.NewOrderDaoByDB(tx).GetOrderById(req.OrderId, uId)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		if order.Type != consts.OrderWaitPay {
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

		userDao := user.NewUserDaoByDB(tx)
		buyer, err := userDao.GetUserById(uId)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		userMoney, err := buyer.DecryptMoney(req.Key)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if userMoney-totalMoney < 0 {
			log.LogrusObj.Error("金额不足")
			return errors.New("金额不足")
		}

		buyer.Money = strconv.FormatInt(userMoney-totalMoney, 10)
		buyer.Money, err = buyer.EncryptMoney(req.Key)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		err = userDao.UpdateUserById(uId, buyer)
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

		productDao := product.NewProductDaoByDB(tx)
		prod, err := productDao.GetProductById(productID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if prod.Num-num < 0 {
			log.LogrusObj.Error("存在超卖问题")
			return errors.New("存在超卖问题")
		}
		prod.Num -= num
		err = productDao.UpdateProduct(productID, prod)
		if err != nil { // 更新商品数量减少失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		// 更新订单状态
		order.Type = consts.OrderWaitShip
		err = orderpkg.NewOrderDaoByDB(tx).UpdateOrderById(req.OrderId, uId, order)
		if err != nil { // 更新订单失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		productUser := product.Product{
			Name:          prod.Name,
			CategoryID:    prod.CategoryID,
			Title:         prod.Title,
			Info:          prod.Info,
			ImgPath:       prod.ImgPath,
			Price:         prod.Price,
			DiscountPrice: prod.DiscountPrice,
			Num:           num,
			OnSale:        false,
			BossID:        uId,
			BossName:      buyer.UserName,
			BossAvatar:    buyer.Avatar,
		}

		err = productDao.CreateProduct(&productUser)
		if err != nil { // 买完商品后创建成了自己的商品失败。订单失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		// outbox 事件：order.paid，事件投递交给 publisher 异步处理
		return outbox.NewOutboxDaoByDB(tx).Insert(
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
		return nil, err
	}

	// TX 已经把 product.Num 真正扣减了；同步把 Redis reserved 桶减掉
	if paidProductID > 0 && paidNum > 0 {
		if cErr := cache.CommitReservation(ctx, paidProductID, int64(paidNum)); cErr != nil {
			log.LogrusObj.Errorf("commit reservation failed product=%d num=%d err=%v", paidProductID, paidNum, cErr)
		}
	}

	return nil, nil
}
