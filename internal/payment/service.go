package payment

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
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

		paidProductID = order.ProductID
		paidNum = order.Num
		// 实付口径（命中满减取折后 FinalCents）统一收口到 orderPayableCents，三条渠道一致。
		payable := orderPayableCents(order)

		userDao := user.NewUserDaoByDB(tx)
		// 支付确认阶段只扣买家，不再提前给卖家入账；因此这里只锁买家。
		// 卖家余额会在订单确认收货后的 order.completed 结算事务中单独加锁并入账。
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

		// 清算：买家 debit，商户托管账户 credit，并记录一笔待结算清算单。
		// 此时卖家余额不变；只有履约完成后才会由 order.completed 消费者放款。
		if err = clearing.RecordClearedTx(tx, order, clearing.ChannelWallet, "", "CNY", &buyerBalanceAfter); err != nil {
			log.LogrusObj.Error(err)
			return err
		}

		// 资金已进入托管，余下"扣库存 → 标记已付 → 商品归属转移 → outbox order.paid"走共享尾段。
		return finishPaymentConfirmationTx(tx, order)
	})

	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	// TX 已经把 product.Num 真正扣减了；同步把 Redis reserved 桶减掉
	commitReservationBestEffort(ctx, paidProductID, paidNum)

	return nil, nil
}
