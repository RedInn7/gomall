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

		bossID := order.BossID
		paidProductID = order.ProductID
		paidNum = order.Num
		// 实付口径（命中满减取折后 FinalCents）统一收口到 orderPayableCents，三条渠道一致。
		payable := orderPayableCents(order)

		userDao := user.NewUserDaoByDB(tx)
		// 统一锁序：按 user id 升序对买卖双方一并加 FOR UPDATE，消除 A 向 B 下单与 B 向 A
		// 下单并发时“先买家后卖家”角色锁序构成的锁环死锁。锁就位后再校验支付密码、读改写余额。
		buyer, boss, err := userDao.LockTwoUsersForUpdate(uId, bossID)
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

		// boss 已在事务开头随 buyer 一并按 id 序加锁，无需二次读取。
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

		// 资金已划转，余下"扣库存 → 标记已付 → 商品归属转移 → outbox order.paid"是三条渠道共享尾段。
		return finishOrderSettlementTx(tx, order)
	})

	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	// TX 已经把 product.Num 真正扣减了；同步把 Redis reserved 桶减掉
	commitReservationBestEffort(ctx, paidProductID, paidNum)

	return nil, nil
}
