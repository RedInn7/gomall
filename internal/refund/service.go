package refund

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
	baseDao := orderpkg.NewOrderDao(ctx)
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
		return orderpkg.ErrInvalidOrderStateTransition
	}
	fromType := order.Type
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := orderpkg.NewOrderDaoByDB(tx).RequestRefund(orderNum, refundAllowedFrom)
		if err != nil {
			return err
		}
		if !ok {
			return orderpkg.ErrInvalidOrderStateTransition
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
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
//   - 写 outbox(order.refunded)，amount 取实付口径 FinalCents（回退到 Money*Num）；tx_id 留空待下游回填
//
// 真正的资金回退在下游 wallet 服务消费事件时执行；本服务仅做状态推进。
func (s *RefundSrv) ApproveRefund(ctx context.Context, orderNum uint64) error {
	baseDao := orderpkg.NewOrderDao(ctx)
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return errors.New("订单不存在")
	}
	if order.Type != consts.OrderRefunding {
		return orderpkg.ErrInvalidOrderStateTransition
	}
	// 退款额取实付口径，与 payment 侧保持一致：命中满减时以折后实付 FinalCents 为准，
	// 仅当 FinalCents 未写入（<=0）时回退到折前价 Money*Num。
	// 用折前价会把满减优惠重复退还（promo 已随事件退预算，钱包再按原价退钱即双重退还）。
	amount := order.FinalCents
	if amount <= 0 {
		amount = order.Money * int64(order.Num)
	}
	txErr := baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := orderpkg.NewOrderDaoByDB(tx).ApproveRefund(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return orderpkg.ErrInvalidOrderStateTransition
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderRefunded", "order.refunded", order.ID,
			events.OrderRefundedEvent{
				OrderID:            order.ID,
				OrderNum:           order.OrderNum,
				UserID:             order.UserID,
				Amount:             amount,
				TxID:               "",
				PromoRuleID:        order.PromoRuleID,
				PromoDiscountCents: order.PromoDiscountCents,
			},
		)
	})
	if txErr != nil {
		return txErr
	}

	// 满减预算退还不在这里同步执行：order.refunded 事件已携带 promo_rule_id /
	// promo_discount_cents，由 promo 侧消费该事件异步完成（at-least-once + 幂等台账）。
	return nil
}

// RejectRefund 运营驳回退款，订单回到 Completed。
//   - 仅允许 Refunding -> Completed
//   - 写 outbox(order.refund_rejected) 让客服 / 用户系统得知
func (s *RefundSrv) RejectRefund(ctx context.Context, orderNum uint64, reason string) error {
	baseDao := orderpkg.NewOrderDao(ctx)
	order, err := baseDao.GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if order == nil || order.ID == 0 {
		return errors.New("订单不存在")
	}
	if order.Type != consts.OrderRefunding {
		return orderpkg.ErrInvalidOrderStateTransition
	}
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := orderpkg.NewOrderDaoByDB(tx).RejectRefund(orderNum)
		if err != nil {
			return err
		}
		if !ok {
			return orderpkg.ErrInvalidOrderStateTransition
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
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

// refundAmount 退款额取实付口径，与 payment 侧建会话 / 结算保持一致：
// 命中促销(PromoRuleID!=0)即以折后实付 FinalCents 为准，否则回退折前价 Money*Num。
// 不能用 FinalCents>0 判：满减全额抵扣到 0 时 FinalCents==0 是合法实付，用 >0 会误回退折前全价多退。
func refundAmount(o *orderpkg.Order) int64 {
	if o.PromoRuleID != 0 {
		return o.FinalCents
	}
	return o.Money * int64(o.Num)
}

// SettleRefund 真正落地一笔已获批退款的资金回退，由 order.refunded 消费者驱动，全程单事务原子：
//  1. 幂等守卫：订单须已处于 Refunded 终态（ApproveRefund 已推进）；台账 (order_id, credit, refund)
//     唯一索引 + 入账前存在性预检共同保证同一订单只退一次，重复投递幂等返回 nil。
//  2. 买家 credit：解密余额 + 退款额，加密写回，追加 credit 流水(BizTypeRefund)。
//  3. 卖家 debit：解密余额 - 退款额，加密写回，追加 debit 流水(BizTypeRefund)。
//  4. 库存回补：把订单数量加回 product.Num。
//
// 取舍：买家一定能退到钱优先。卖家可能已提现导致余额不足，这里仍按需扣（即使记账余额转负也照记），
// 由平台清算兜底负差，绝不因卖家余额不足而阻塞买家退款。
//
// 不做项（留待后续）：买家购买时生成的商品归属副本不在此回退——买家可能已改价 / 已转售，
// 删除副本不安全，需配套的副本溯源与人工/对账流程，本期不动。
func (s *RefundSrv) SettleRefund(ctx context.Context, orderID uint) error {
	db := dao.NewDBClient(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		orderDao := orderpkg.NewOrderDaoByDB(tx)
		o, err := orderDao.GetOrderByIdOnly(orderID)
		if err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		if o == nil || o.ID == 0 {
			// 订单不存在：脏事件，幂等放行不阻塞队列。
			util.LogrusObj.Warnf("refund settle skip: order=%d not found", orderID)
			return nil
		}
		// 状态守卫：仅结算已获批（Refunded 终态）的订单。其它状态（含 Refunding 尚未获批）一律幂等放行，
		// 不在此推进状态机——状态推进归 ApproveRefund，避免双写冲突。
		if o.Type != consts.OrderRefunded {
			util.LogrusObj.Infof("refund settle skip: order=%d state=%d not refunded", orderID, o.Type)
			return nil
		}

		ledgerDao := money.NewLedgerDaoByDB(tx)
		// 幂等预检：买家 credit 流水已存在即视为已退过，直接放行（唯一索引为最终兜底）。
		var settled int64
		if err = tx.Model(&money.AccountTransaction{}).
			Where("ref_order_id=? AND direction=? AND biz_type=?", orderID, money.DirectionCredit, money.BizTypeRefund).
			Count(&settled).Error; err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		if settled > 0 {
			util.LogrusObj.Infof("refund settle skip: order=%d already settled", orderID)
			return nil
		}

		amount := refundAmount(o)
		if amount <= 0 {
			// 实付为 0（如满减全额抵扣）：无资金可退，仅回补库存后返回。
			if _, rerr := product.NewProductDaoWithDB(tx).RollbackStock(o.ProductID, o.Num); rerr != nil {
				util.LogrusObj.Error(rerr)
				return rerr
			}
			return nil
		}

		userDao := user.NewUserDaoByDB(tx)

		// 买家 credit：行锁读 -> 解密 +amount -> 加密写回 -> 追加 credit 流水。
		buyer, err := userDao.GetUserByIdForUpdate(o.UserID)
		if err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		buyerBal, err := buyer.DecryptMoney()
		if err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		buyerAfter := buyerBal + amount
		buyer.Money = strconv.FormatInt(buyerAfter, 10)
		if buyer.Money, err = buyer.EncryptMoney(); err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		if err = userDao.UpdateUserById(o.UserID, buyer); err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		if err = ledgerDao.AppendTransaction(o.UserID, o.ID, money.DirectionCredit, amount, buyerAfter, money.BizTypeRefund); err != nil {
			util.LogrusObj.Error(err)
			return err
		}

		// 卖家 debit：行锁读 -> 解密 -amount -> 加密写回 -> 追加 debit 流水。
		// 卖家可能已提现使余额不足，按"买家优先"取舍仍照扣，记账余额转负由平台清算兜底。
		seller, err := userDao.GetUserByIdForUpdate(o.BossID)
		if err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		sellerBal, err := seller.DecryptMoney()
		if err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		sellerAfter := sellerBal - amount
		seller.Money = strconv.FormatInt(sellerAfter, 10)
		if seller.Money, err = seller.EncryptMoney(); err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		if err = userDao.UpdateUserById(o.BossID, seller); err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		if err = ledgerDao.AppendTransaction(o.BossID, o.ID, money.DirectionDebit, amount, sellerAfter, money.BizTypeRefund); err != nil {
			util.LogrusObj.Error(err)
			return err
		}

		// 库存回补：把订单数量加回在售库存（支付时已扣减，退款是其逆操作）。
		if _, err = product.NewProductDaoWithDB(tx).RollbackStock(o.ProductID, o.Num); err != nil {
			util.LogrusObj.Error(err)
			return err
		}

		// 商品归属副本回退不做，原因见方法注释。
		return nil
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
