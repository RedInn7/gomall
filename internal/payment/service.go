package payment

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
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
		// 实付口径：命中满减时以折后实付 FinalCents 为准，否则单价 * 件数。
		// 之前一律用 order.Money*num（折前价）会把满减优惠吞掉，买家被多扣。
		// 判据用 PromoRuleID（与下单侧一致的命中口径），不能用 FinalCents > 0：
		// 满减立减到 0 / 100% 折扣时 FinalCents == 0 是合法实付，误判为未命中会回退全价。
		payable := order.Money * int64(num)
		if order.PromoRuleID != 0 {
			payable = order.FinalCents
		}

		userDao := user.NewUserDaoByDB(tx)
		// 先校验支付密码（与余额加密分离），再做加行锁的余额读改写
		buyer, err := userDao.GetUserByIdForUpdate(uId)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if !buyer.CheckMoneyPassword(req.Key) {
			log.LogrusObj.Error(user.ErrMoneyKeyIncorrect)
			return user.ErrMoneyKeyIncorrect
		}

		userMoney, err := buyer.DecryptMoney()
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if userMoney-payable < 0 {
			log.LogrusObj.Error("金额不足")
			return errors.New("金额不足")
		}

		buyerBalanceAfter := userMoney - payable
		buyer.Money = strconv.FormatInt(buyerBalanceAfter, 10)
		buyer.Money, err = buyer.EncryptMoney()
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		err = userDao.UpdateUserById(uId, buyer)
		if err != nil { // 更新用户金额失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		// 买家扣款流水：与余额变动同事务追加，(order_id, debit) 唯一索引兜底重复扣款
		ledgerDao := money.NewLedgerDaoByDB(tx)
		if err = ledgerDao.AppendTransaction(uId, order.ID, money.DirectionDebit, payable, buyerBalanceAfter, money.BizTypeOrderPay); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		boss, err := userDao.GetUserByIdForUpdate(bossID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		// 商家余额同样用服务端密钥解密——绝不能用买家支付密码，否则跨账户串密钥
		bossMoney, err := boss.DecryptMoney()
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		bossBalanceAfter := bossMoney + payable
		boss.Money = strconv.FormatInt(bossBalanceAfter, 10)
		boss.Money, err = boss.EncryptMoney()
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		err = userDao.UpdateUserById(bossID, boss)
		if err != nil { // 更新boss金额失败，回滚
			log.LogrusObj.Error(err)
			return err
		}

		// 卖家入账流水：方向与买家相反，(order_id, credit) 唯一索引兜底重复入账
		if err = ledgerDao.AppendTransaction(bossID, order.ID, money.DirectionCredit, payable, bossBalanceAfter, money.BizTypeOrderPay); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		productDao := product.NewProductDaoByDB(tx)
		prod, err := productDao.GetProductById(productID)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		// 原子扣减库存：条件 UPDATE 一步到位，消除"读-判断-写"并发超卖
		ok, err := product.NewProductDaoWithDB(tx).DeductStock(productID, num)
		if err != nil {
			log.LogrusObj.Error(err)
			return err
		}
		if !ok {
			log.LogrusObj.Error("存在超卖问题")
			return errors.New("存在超卖问题")
		}

		// 更新订单状态：条件 UPDATE 把 WaitPay 守卫塞进 WHERE，杜绝重复支付
		paidOK, err := orderpkg.NewOrderDaoByDB(tx).MarkOrderPaidWithCheck(req.OrderId, uId)
		if err != nil { // 更新订单失败，回滚
			log.LogrusObj.Error(err)
			return err
		}
		if !paidOK {
			log.LogrusObj.Error("订单状态已变更，无法重复支付")
			return errors.New("订单状态已变更，无法重复支付")
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
