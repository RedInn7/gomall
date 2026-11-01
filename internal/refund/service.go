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
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
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

// afterSalesWindow 确认收货后多久内还能申请退款。超过之后卖家已经结算、货也用了很久，
// 再从卖家余额扣钱不合理，走人工客服。
const afterSalesWindow = 15 * 24 * time.Hour

// maxRefundRequests 同一订单最多申请几次退款。每次驳回都回到原状态，不设上限买家就能反复申请骚扰商家。
const maxRefundRequests = 3

var (
	// ErrAfterSalesExpired 已完成订单超过售后时效。
	ErrAfterSalesExpired = errors.New("已超过售后期，请联系客服")
	// ErrTooManyRefundRequests 同一订单退款申请次数到上限。
	ErrTooManyRefundRequests = errors.New("该订单退款申请次数已达上限，请联系客服")
	// ErrSellerBalanceInsufficient 结算后退款要从卖家余额扣，余额不够时不扣成负数，留给重试 / 补偿任务。
	ErrSellerBalanceInsufficient = errors.New("卖家余额不足，退款待卖家补足后自动完成")
	// ErrNotReturnable 不是"已发货后退款且未回补库存"的订单，不能确认退货入库。
	ErrNotReturnable = errors.New("该订单没有待入库的退货")
)

// RequestRefund 用户发起退款申请。
//   - from 必须落在 refundAllowedFrom：WaitShip / WaitReceive / Completed，且状态机允许 from -> Refunding
//   - 已完成订单只能在 afterSalesWindow 内申请；同一订单最多申请 maxRefundRequests 次
//   - 校验 ctx 中的 userID 与订单归属一致
//   - FOR UPDATE 锁住订单后再判断、再条件 UPDATE，避免读后写竞态
//   - reason 落订单表，商家审批时要看
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
			return orderpkg.ErrOrderForbidden
		}
		fromType := order.Type
		if !inUintSlice(fromType, refundAllowedFrom) || !orderpkg.CanTransition(fromType, consts.OrderRefunding) {
			return orderpkg.ErrInvalidOrderStateTransition
		}
		if fromType == consts.OrderCompleted {
			completedAt := order.UpdatedAt
			if order.CompletedAt != nil {
				completedAt = *order.CompletedAt
			}
			if time.Since(completedAt) > afterSalesWindow {
				return ErrAfterSalesExpired
			}
		}
		if order.RefundCount >= maxRefundRequests {
			return ErrTooManyRefundRequests
		}
		ok, err := orderDao.RequestRefund(orderNum, fromType, reason, maxRefundRequests)
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
//   - 商家只能审批自己的订单（MerchantScope），admin 不限
//   - 仅允许 Refunding -> Refunded
//   - 写 outbox(order.refunded)，amount 与实际退款同一口径（refundAmount）；tx_id 留空待下游回填
//
// 真正的资金回退在下游 wallet 服务消费事件时执行；本服务仅做状态推进。
func (s *RefundSrv) ApproveRefund(ctx context.Context, orderNum uint64) error {
	bossID, err := orderpkg.MerchantScope(ctx)
	if err != nil {
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
	if bossID != 0 && order.BossID != bossID {
		return orderpkg.ErrOrderForbidden
	}
	if order.Type != consts.OrderRefunding {
		return orderpkg.ErrInvalidOrderStateTransition
	}
	// 事件金额与 SettleRefund 实际退的钱同一口径，满减抵扣到 0 的订单事件上也是 0，不是原价。
	amount := refundAmount(order)
	txErr := baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := orderpkg.NewOrderDaoByDB(tx).ApproveRefund(orderNum, bossID)
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
//   - 商家只能驳回自己的订单（MerchantScope），admin 不限
//   - 仅允许 Refunding -> RefundFromType（WaitShip / WaitReceive / Completed）
//   - 写 outbox(order.refund_rejected) 让客服 / 用户系统得知
func (s *RefundSrv) RejectRefund(ctx context.Context, orderNum uint64, reason string) error {
	bossID, err := orderpkg.MerchantScope(ctx)
	if err != nil {
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
	if bossID != 0 && order.BossID != bossID {
		return orderpkg.ErrOrderForbidden
	}
	if order.Type != consts.OrderRefunding {
		return orderpkg.ErrInvalidOrderStateTransition
	}
	restoreType := order.RefundFromType
	if !inUintSlice(restoreType, refundAllowedFrom) {
		// 兼容迁移前已经处于 Refunding、没有记录来源状态的旧订单，沿用旧行为恢复 Completed。
		restoreType = consts.OrderCompleted
	}
	if !orderpkg.CanTransition(consts.OrderRefunding, restoreType) {
		return orderpkg.ErrInvalidOrderStateTransition
	}
	return baseDao.DB.Transaction(func(tx *gorm.DB) error {
		ok, err := orderpkg.NewOrderDaoByDB(tx).RejectRefund(orderNum, restoreType, bossID)
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
	var restocked *orderpkg.Order
	db := dao.NewDBClient(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
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
			if err = userDao.UpdateUserMoney(o.UserID, buyer.Money); err != nil {
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
			if sellerAfter < 0 {
				// 不把卖家扣成负数（已提现时就成了平台坏账）。返回错误让消费者重试、进 DLQ 后由
				// RunSettleCompensation 每轮再试，卖家余额补足后自动退给买家。
				util.LogrusObj.Errorf("refund settle blocked: seller=%d balance=%d < refund=%d order=%d", o.BossID, sellerBal, amount, o.ID)
				return ErrSellerBalanceInsufficient
			}
			if amount > 0 {
				seller.Money = strconv.FormatInt(sellerAfter, 10)
				if seller.Money, err = seller.EncryptMoney(); err != nil {
					return err
				}
				if err = userDao.UpdateUserMoney(o.BossID, seller.Money); err != nil {
					return err
				}
			}
			if err = ledgerDao.AppendTransaction(o.BossID, o.ID, money.DirectionDebit, amount, sellerAfter, money.BizTypeRefund); err != nil {
				return err
			}
		}

		// 库存回补只给未发货就退款的订单：货还在仓库里。已发货的货在途或在买家手里，
		// 这时回补会超卖，要等商家确认退货入库（ConfirmReturnReceived）再补。
		ok, err := orderDao.MarkRestocked(o.ID, 0, true)
		if err != nil {
			return err
		}
		if ok {
			if _, err = product.NewProductDaoWithDB(tx).RollbackStock(o.ProductID, o.Num); err != nil {
				util.LogrusObj.Error(err)
				return err
			}
			restocked = o
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
	if err != nil || restocked == nil {
		return err
	}
	restockRedis(ctx, restocked)
	return nil
}

// restockRedis DB 提交后再把回补的件数加回 Redis available，否则缓存里这批库存卖不出去。
// 失败只记日志：DB 已是真相，交给库存对账。
func restockRedis(ctx context.Context, o *orderpkg.Order) {
	if e := cache.RestockAvailable(ctx, o.ProductID, int64(o.Num)); e != nil {
		util.LogrusObj.Errorf("refund restock redis failed order=%d product=%d num=%d err=%v",
			o.ID, o.ProductID, o.Num, e)
	}
}

// ConfirmReturnReceived 商家确认退货已入库，回补库存。只对已退款、发货后才退的订单有效：
// 未发货的退款在结算时已经回补。MarkRestocked 条件抢占保证并发 / 重复点击只补一次。
// 仅退款不退货的订单商家不点这个，库存也就不会凭空多出来。
func (s *RefundSrv) ConfirmReturnReceived(ctx context.Context, orderNum uint64) error {
	bossID, err := orderpkg.MerchantScope(ctx)
	if err != nil {
		return err
	}
	o, err := orderpkg.NewOrderDao(ctx).GetOrderByOrderNum(orderNum)
	if err != nil {
		return err
	}
	if o == nil || o.ID == 0 {
		return errors.New("订单不存在")
	}
	if bossID != 0 && o.BossID != bossID {
		return orderpkg.ErrOrderForbidden
	}
	err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := orderpkg.NewOrderDaoByDB(tx).MarkRestocked(o.ID, bossID, false)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotReturnable
		}
		_, err = product.NewProductDaoWithDB(tx).RollbackStock(o.ProductID, o.Num)
		return err
	})
	if err != nil {
		return err
	}
	restockRedis(ctx, o)
	return nil
}

// ListRefunding 商家查看待审批的退款单（含买家填的原因、申请次数），admin 看全部。按 id 倒序，last_id 翻页。
func (s *RefundSrv) ListRefunding(ctx context.Context, lastID uint, pageSize int) ([]*orderpkg.Order, error) {
	bossID, err := orderpkg.MerchantScope(ctx)
	if err != nil {
		return nil, err
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	q := dao.NewDBClient(ctx).Model(&orderpkg.Order{}).Where("type = ?", consts.OrderRefunding)
	if bossID != 0 {
		q = q.Where("boss_id = ?", bossID)
	}
	if lastID > 0 {
		q = q.Where("id < ?", lastID)
	}
	var orders []*orderpkg.Order
	err = q.Order("id DESC").Limit(pageSize).Find(&orders).Error
	return orders, err
}

// settleCompensationDelay 补偿任务只碰这么久之前就该结算完的订单，给正常的事件链路留出时间。
const settleCompensationDelay = 10 * time.Minute

// RunSettleCompensation cron 资金兜底：order.completed / order.refunded 事件丢了（outbox 死信、消费者进 DLQ），
// 卖家就永远拿不到钱、买家永远退不到款。这里直接按订单与清算单状态补：已完成但清算单还是 cleared 的补结算，
// 已退款但清算单还不是 refunded 的补退款。两边都按清算单状态幂等，与正常消费者并发也只生效一次。
// ponytail: 每轮各补 100 单，永久失败的单会一直占名额；量大时改成按 id 游标翻页。
func (s *RefundSrv) RunSettleCompensation() {
	defer func() {
		if r := recover(); r != nil {
			util.LogrusObj.Errorf("SettleCompensation Cron Panic: %v", r)
		}
	}()
	ctx := context.Background()
	db := dao.NewDBClient(ctx)
	before := time.Now().Add(-settleCompensationDelay)
	missed := func(orderType uint, clearingCond, clearingStatus string) []uint {
		var ids []uint
		if err := db.Model(&orderpkg.Order{}).
			Where("type = ? AND updated_at <= ? AND id IN (?)", orderType, before,
				db.Model(&clearing.PaymentClearing{}).Select("order_id").Where(clearingCond, clearingStatus)).
			Limit(100).Pluck("id", &ids).Error; err != nil {
			util.LogrusObj.Errorf("SettleCompensation 扫表失败 type=%d err=%v", orderType, err)
		}
		return ids
	}
	for _, id := range missed(consts.OrderCompleted, "status = ?", clearing.StatusCleared) {
		if err := clearing.SettleCompletedOrder(ctx, id); err != nil {
			util.LogrusObj.Errorf("SettleCompensation 补结算失败 order=%d err=%v", id, err)
		}
	}
	for _, id := range missed(consts.OrderRefunded, "status <> ?", clearing.StatusRefunded) {
		if err := s.SettleRefund(ctx, id); err != nil {
			util.LogrusObj.Errorf("SettleCompensation 补退款失败 order=%d err=%v", id, err)
		}
	}
	s.compensatePromoRelease(ctx, db, before)
}

// promoReleaseLookback 满减预算补退只看这么久之内关单 / 退款的订单。
const promoReleaseLookback = 24 * time.Hour

// compensatePromoRelease 满减预算退还的兜底：promo.release 消费者重试用完进了 DLQ，预算就永久少退。
// 扫"已关单 / 已退款、命中满减、却没有 promo_release 台账"的订单补退，ReleaseDiscount 按台账唯一键幂等，
// 与正常消费者并发也只退一次。只回看 promoReleaseLookback：预算按天重置（consumed_today），
// 隔天再退是往当天预算里加钱，也会把上线前没有台账的历史订单全部重退一遍。
func (s *RefundSrv) compensatePromoRelease(ctx context.Context, db *gorm.DB, before time.Time) {
	var orders []*orderpkg.Order
	if err := db.Model(&orderpkg.Order{}).
		Where("type IN ? AND promo_rule_id <> 0 AND promo_discount_cents > 0 AND updated_at <= ? AND updated_at >= ?",
			[]uint{consts.OrderClosed, consts.OrderRefunded}, before, time.Now().Add(-promoReleaseLookback)).
		Where("id NOT IN (?)", db.Model(&promo.PromoRelease{}).Select("order_id")).
		Limit(100).Find(&orders).Error; err != nil {
		util.LogrusObj.Errorf("SettleCompensation 扫满减待退还失败 err=%v", err)
		return
	}
	for _, o := range orders {
		reason := "refund"
		if o.Type == consts.OrderClosed {
			reason = "cancel"
		}
		if err := promo.GetPromoSrv().ReleaseDiscount(ctx, o.ID, o.PromoRuleID, o.PromoDiscountCents, reason); err != nil {
			util.LogrusObj.Errorf("SettleCompensation 补退满减预算失败 order=%d err=%v", o.ID, err)
		}
	}
}

func inUintSlice(v uint, s []uint) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
