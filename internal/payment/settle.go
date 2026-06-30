package payment

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
)

// 本文件收口三条支付渠道（余额 / Stripe / Web3）此前各自抄一遍的"确认收款"公共逻辑：
// 应付口径、结算事务尾段、预留核销。各渠道只保留真正差异化的发起流程与资金划转，
// 尾段统一走这里——新增渠道时无法再漏写库存/状态/outbox 任一步，杜绝行为漂移。

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

// finishOrderSettlementTx 跑三条支付渠道共享的结算事务尾段（在调用方完成渠道特定的资金
// 划转之后、同一事务内执行）：
//
//	原子扣减库存（条件 UPDATE 防超卖）→ 标记订单已付（WaitPay 守卫防重复支付）→
//	商品归属转移给买家（名下复制一份下架同款，二手交易模型）→ 写 outbox order.paid。
//
// 幂等：MarkOrderPaidWithCheck 的 WaitPay 守卫为主，配合 (order_id,direction) 台账唯一索引兜底。
// 买家 id 一律取 o.UserID（下单人），与三条渠道原有口径一致。
func finishOrderSettlementTx(tx *gorm.DB, o *order.Order) error {
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
		log.LogrusObj.Error("存在超卖问题")
		return errors.New("存在超卖问题")
	}

	paidOK, err := order.NewOrderDaoByDB(tx).MarkOrderPaidWithCheck(o.ID, buyerID)
	if err != nil {
		log.LogrusObj.Error(err)
		return err
	}
	if !paidOK {
		log.LogrusObj.Error("订单状态已变更，无法重复支付")
		return errors.New("订单状态已变更，无法重复支付")
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
