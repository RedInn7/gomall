package payment

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
)

// 本文件收口三条支付渠道（余额 / Stripe / Web3）此前各自抄一遍的"确认收款"公共逻辑：
// 应付口径、支付确认事务尾段、预留核销。各渠道只保留真正差异化的发起流程与清算，
// 尾段统一走这里——新增渠道时无法再漏写库存/状态/outbox 任一步，杜绝行为漂移。

var (
	errOrderNotPayable    = errors.New("订单状态非未支付，无法发起支付")
	errPreorderNotPayable = errors.New("预售订单请在预售页支付尾款")
	errProductOversold    = errors.New("存在超卖问题")
	errOrderStateChanged  = errors.New("订单状态已变更，无法重复支付")
)

// ensureOrderPayable 普通支付入口（余额 PayDown / Stripe、Xcash 建单 / Web3 取 nonce 与签名）共用的
// 可支付校验。预售单付完定金后 type 仍是 WaitPay，但尾款只能走 preorder 的 PayFinal，
// 在这里按全款再收一次就是重复收费。MarkOrderPaidWithCheck 的 WHERE 另有同一守卫兜底。
func ensureOrderPayable(o *order.Order) error {
	if o.Type != consts.OrderWaitPay {
		return errOrderNotPayable
	}
	if o.PreorderStage != 0 {
		return errPreorderNotPayable
	}
	return nil
}

// orderPayableCents 订单应付金额（分）的统一口径，所有支付渠道共用。
//
//	命中促销（PromoRuleID != 0）以折后实付 FinalCents 为准；否则单价 Money * 件数 Num。
//
// 判据用 PromoRuleID 而非 FinalCents > 0：满减全额抵扣 / 100% 折扣时 FinalCents == 0 是
// 合法实付，用 >0 判会误判为未命中、回退折前全价向买家多扣。与下单侧命中口径保持一致。
func orderPayableCents(o *order.Order) int64 {
	if o.PromoRuleID != 0 {
		return o.FinalCents
	}
	return o.Money * int64(o.Num)
}

// finishPaymentConfirmationTx 跑三条支付渠道共享的“支付确认”事务尾段（在调用方完成清算后、
// 同一事务内执行）。它不向卖家放款；真正的卖家结算由 order.completed 触发 clearing 服务完成：
//
//	原子扣减库存（条件 UPDATE 防超卖）→ 标记订单已付（WaitPay 守卫防重复支付）→
//	商品归属转移给买家（名下复制一份下架同款，二手交易模型）→ 写 outbox order.paid。
//
// 幂等：MarkOrderPaidWithCheck 的 WaitPay 守卫为主，配合 (order_id,direction) 台账唯一索引兜底。
// 买家 id 一律取 o.UserID（下单人），与三条渠道原有口径一致。
func finishPaymentConfirmationTx(tx *gorm.DB, o *order.Order) error {
	productID := o.ProductID
	num := o.Num
	buyerID := o.UserID

	prod, err := product.NewProductDaoByDB(tx).GetProductById(productID)
	if err != nil {
		log.LogrusObj.Error(err)
		return err
	}
	ok, err := product.NewProductDaoWithDB(tx).DeductStock(productID, num)
	if err != nil {
		log.LogrusObj.Error(err)
		return err
	}
	if !ok {
		log.LogrusObj.Error(errProductOversold)
		return errProductOversold
	}

	paidOK, err := order.NewOrderDaoByDB(tx).MarkOrderPaidWithCheck(o.ID, buyerID)
	if err != nil {
		log.LogrusObj.Error(err)
		return err
	}
	if !paidOK {
		log.LogrusObj.Error(errOrderStateChanged)
		return errOrderStateChanged
	}

	buyer, err := user.NewUserDaoByDB(tx).GetUserById(buyerID)
	if err != nil {
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
		BossID:        buyerID,
		BossName:      buyer.UserName,
		BossAvatar:    buyer.Avatar,
	}
	if err := product.NewProductDaoByDB(tx).CreateProduct(&productUser); err != nil {
		log.LogrusObj.Error(err)
		return err
	}

	return outbox.NewOutboxDaoByDB(tx).Insert(
		"order", "OrderPaid", "order.paid", o.ID,
		events.OrderPaid{
			OrderID:   o.ID,
			OrderNum:  o.OrderNum,
			UserID:    buyerID,
			ProductID: productID,
			Num:       num,
		},
	)
}

// isUnfulfillable 支付确认失败是不是"重试也不会成功"的确定性失败：订单 / 商品 / 买家已不存在、
// 库存不足、订单已不在待支付。外部渠道遇到它不能返回错误让渠道无限重投。
func isUnfulfillable(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, errProductOversold) || errors.Is(err, errOrderStateChanged)
}

// recordUnfulfillable 外部渠道（Stripe / Web3）已收款、本地却确定性无法履约时调用：原事务已整体回滚
// （没扣库存、没清算），这里在独立事务里记一条 fulfillment_failed 异常单并返回 nil，让渠道停止重投，
// 由运营走异常审核退款。非确定性失败（DB 抖动、并发唯一键冲突等）原样返回，交给渠道重投。
func recordUnfulfillable(db *gorm.DB, settleErr error, orderID uint, channel, providerRef, amount, currency string) error {
	if !isUnfulfillable(settleErr) {
		return settleErr
	}
	o := &order.Order{}
	o.ID = orderID
	if err := db.Transaction(func(tx *gorm.DB) error {
		return clearing.RecordExternalAnomalyTx(tx, o, channel, providerRef, amount, currency, clearing.AnomalyReasonFulfillmentFailed)
	}); err != nil {
		return err
	}
	log.LogrusObj.Errorf("%s payment recorded for refund review, order cannot be fulfilled: order=%d ref=%s err=%v",
		channel, orderID, providerRef, settleErr)
	return nil
}

// commitReservationBestEffort 在结算事务提交后核销 Redis 预留（reserved）桶。
// DB 已真正扣减 product.Num，这里只同步缓存视图；失败仅记日志，绝不回滚已落库的支付。
func commitReservationBestEffort(ctx context.Context, productID uint, num int) {
	if productID == 0 || num <= 0 {
		return
	}
	if err := cache.CommitReservation(ctx, productID, int64(num)); err != nil {
		log.LogrusObj.Errorf("commit reservation failed product=%d num=%d err=%v", productID, num, err)
	}
}
