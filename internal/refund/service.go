package refund

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
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
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		orderDao := orderpkg.NewOrderDaoByDB(tx)
		order, err := orderDao.GetOrderByOrderNumForUpdate(orderNum)
		if err != nil {
			return err
		}
		if order == nil || order.ID == 0 {
			return errors.New("订单不存在")
		}
		if order.UserID != u.Id {
			return errors.New("无权操作该订单")
		}
		fromType := order.Type
		if !inUintSlice(fromType, refundAllowedFrom) {
			return orderpkg.ErrInvalidOrderStateTransition
		}
		ok, err := orderDao.RequestRefund(orderNum, fromType)
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

// RejectRefund 运营驳回退款，订单回到申请退款前的状态。
//   - 仅允许 Refunding -> RefundFromType（WaitShip / WaitReceive / Completed）
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
	restoreType := order.RefundFromType
	if !inUintSlice(restoreType, refundAllowedFrom) {
		// 兼容迁移前已经处于 Refunding、没有记录来源状态的旧订单，沿用旧行为恢复 Completed。
		restoreType = consts.OrderCompleted
	}
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := orderpkg.NewOrderDaoByDB(tx).RejectRefund(orderNum, restoreType)
		if err != nil {
			return err
		}
		if !ok {
			return orderpkg.ErrInvalidOrderStateTransition
		}
		return outbox.NewOutboxDaoByDB(tx).Insert(
			"order", "OrderRefundRejected", "order.refund_rejected", order.ID,
			events.OrderRefundRejected{
				OrderID:      order.ID,
				OrderNum:     order.OrderNum,
				UserID:       order.UserID,
				Reason:       reason,
				RestoredType: restoreType,
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

// SettleRefund 真正落地一笔已获批退款，由 order.refunded 消费者驱动，全程单事务原子。
// 资金来源由清算单决定：尚在托管时从 merchant_escrow 退；已经结算给卖家时才从卖家余额扣。
// 这样 WaitShip / WaitReceive 阶段的退款不会制造卖家负余额，重复事件由清算状态与台账唯一键共同吸收。
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
		var clearingRecord clearing.PaymentClearing
		clearingFound := true
		if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_id = ?", orderID).First(&clearingRecord).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// 兼容迁移前已经支付的旧订单：没有清算单时按“已结算给卖家”处理。
				clearingFound = false
			} else {
				return err
			}
		}
		if clearingFound && clearingRecord.Status == clearing.StatusRefunded {
			return nil
		}
		if clearingFound && clearingRecord.Status != clearing.StatusCleared && clearingRecord.Status != clearing.StatusSettled {
			return fmt.Errorf("%w: %s", clearing.ErrInvalidClearingState, clearingRecord.Status)
		}
		fromEscrow := clearingFound && clearingRecord.Status == clearing.StatusCleared
		refundBizType := money.BizTypeRefund
		if fromEscrow {
			refundBizType = money.BizTypeEscrowRefund
		}

		// 幂等预检：买家 credit 流水已存在即视为已退过，直接放行（唯一索引为最终兜底）。
		var settled int64
		if err = tx.Model(&money.AccountTransaction{}).
			Where("ref_order_id=? AND direction=? AND biz_type=?", orderID, money.DirectionCredit, refundBizType).
			Count(&settled).Error; err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		if settled > 0 {
			return nil
		}

		amount := refundAmount(o)
		if clearingFound {
			// 清算单是资金事实，不能在退款时仅靠可能被修改的订单字段重新算钱。
			// 当前尚未实现手续费拆分退款，因此 fee!=0 或 gross!=net 时拒绝自动处理，交人工对账。
			if clearingRecord.FeeCents != 0 || clearingRecord.GrossCents != clearingRecord.NetCents {
				return errors.New("退款暂不支持含手续费的清算单")
			}
			if amount != clearingRecord.GrossCents {
				return clearing.ErrClearingOrderMismatch
			}
			amount = clearingRecord.GrossCents
		}
		if amount < 0 {
			return errors.New("退款金额非法")
		}
		userDao := user.NewUserDaoByDB(tx)

		// 买家 credit：即使实付为 0，也写一组 0 元幂等流水，避免重复消息反复回补库存。
		buyer, err := userDao.GetUserByIdForUpdate(o.UserID)
		if err != nil {
			return err
		}
		buyerBal, err := buyer.DecryptMoney()
		if err != nil {
			return err
		}
		buyerAfter := buyerBal + amount
		if amount > 0 {
			buyer.Money = strconv.FormatInt(buyerAfter, 10)
			if buyer.Money, err = buyer.EncryptMoney(); err != nil {
				return err
			}
			if err = userDao.UpdateUserById(o.UserID, buyer); err != nil {
				return err
			}
		}
		if err = ledgerDao.AppendTransaction(o.UserID, o.ID, money.DirectionCredit, amount, buyerAfter, refundBizType); err != nil {
			return err
		}

		if fromEscrow {
			// 履约前退款：托管款尚未属于卖家，直接冲回 escrow。
			if err = ledgerDao.AppendSystemTransaction(money.AccountCodeMerchantEscrow, o.ID, money.DirectionDebit, amount, money.BizTypeEscrowRefund); err != nil {
				return err
			}
		} else {
			// 履约后退款：卖家已经收款，才从卖家余额扣回。
			seller, err := userDao.GetUserByIdForUpdate(o.BossID)
			if err != nil {
				return err
			}
			sellerBal, err := seller.DecryptMoney()
			if err != nil {
				return err
			}
			sellerAfter := sellerBal - amount
			if amount > 0 {
				seller.Money = strconv.FormatInt(sellerAfter, 10)
				if seller.Money, err = seller.EncryptMoney(); err != nil {
					return err
				}
				if err = userDao.UpdateUserById(o.BossID, seller); err != nil {
					return err
				}
			}
			if err = ledgerDao.AppendTransaction(o.BossID, o.ID, money.DirectionDebit, amount, sellerAfter, money.BizTypeRefund); err != nil {
				return err
			}
		}

		// 库存回补：把订单数量加回在售库存（支付时已扣减，退款是其逆操作）。
		if _, err = product.NewProductDaoWithDB(tx).RollbackStock(o.ProductID, o.Num); err != nil {
			util.LogrusObj.Error(err)
			return err
		}
		if clearingFound {
			now := time.Now()
			if err = tx.Model(&clearing.PaymentClearing{}).Where("id = ?", clearingRecord.ID).
				Updates(map[string]interface{}{"status": clearing.StatusRefunded, "refunded_at": &now}).Error; err != nil {
				return err
			}
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
