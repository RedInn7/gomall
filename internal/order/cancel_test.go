package order

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
)

// TestOrderCancel_PromoReleaseClosedLoop 关单后的满减预算退还走异步链路：
//  1. CancelUnpaidOrder 只负责状态推进 + 写 order.cancelled 事件，
//     payload 自包含 promo_rule_id / promo_discount_cents；
//  2. 把 outbox payload 交给 promo 消费 handler 完成投递闭环 → 预算回补 + 台账落地；
//  3. 重复投递（at-least-once）不会二次回补。
func TestOrderCancel_PromoReleaseClosedLoop(t *testing.T) {
	initLogForTest()
	db, restore := setupSQLiteForOrder(t)
	defer restore()
	cleanupRedis := setupRedisForOrder(t)
	defer cleanupRedis()
	ensureSnowflakeForOrder()

	p := seedOrderProduct(t, db, "p-cancel-promo", 10000)
	rule := seedPromoRule(t, db, "满 80 减 10",
		promo.PromoRuleTypeAmount, promo.PromoScopeAll, 0,
		8000, 1000, 0, 5000)
	// 该订单下单时已扣过 1000 分预算，等待关单后退还
	if err := db.Model(&promo.PromoRule{}).
		Where("id = ?", rule.ID).
		Update("consumed_today", 1000).Error; err != nil {
		t.Fatalf("seed consumed_today: %v", err)
	}

	const orderNum = uint64(920001)
	o := &Order{
		UserID:             1,
		ProductID:          p.ID,
		BossID:             1,
		AddressID:          1,
		Num:                1,
		OrderNum:           orderNum,
		Type:               consts.OrderWaitPay,
		Money:              10000,
		PromoRuleID:        rule.ID,
		PromoDiscountCents: 1000,
		FinalCents:         9000,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	// 下单时的库存预占，关单时由 CancelUnpaidOrder 释放
	if err := cache.ReserveStock(context.Background(), p.ID, 1); err != nil {
		t.Fatalf("reserve stock: %v", err)
	}

	if err := CancelUnpaidOrder(orderNum); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	var dbOrder Order
	if err := db.First(&dbOrder, o.ID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if dbOrder.Type != consts.OrderClosed {
		t.Fatalf("order type = %d, want %d (closed)", dbOrder.Type, consts.OrderClosed)
	}

	// 关单事务内不再同步退预算：此时 consumed_today 应保持原值
	var dbRule promo.PromoRule
	if err := db.First(&dbRule, rule.ID).Error; err != nil {
		t.Fatalf("reload rule: %v", err)
	}
	if dbRule.ConsumedToday != 1000 {
		t.Fatalf("关单事务内 consumed_today 应保持 1000，got %d", dbRule.ConsumedToday)
	}

	// outbox 的 order.cancelled payload 必须自包含满减字段
	var evtRow outbox.OutboxEvent
	if err := db.Where("routing_key = ? AND aggregate_id = ?", "order.cancelled", o.ID).
		First(&evtRow).Error; err != nil {
		t.Fatalf("load order.cancelled outbox: %v", err)
	}
	var evt events.OrderCancelled
	if err := json.Unmarshal([]byte(evtRow.Payload), &evt); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if evt.PromoRuleID != rule.ID || evt.PromoDiscountCents != 1000 {
		t.Fatalf("payload promo 字段不符: rule=%d discount=%d", evt.PromoRuleID, evt.PromoDiscountCents)
	}

	// 投递闭环：把 outbox payload 交给 promo 消费 handler，预算应回补
	ctx := context.Background()
	if err := promo.DispatchReleaseEvent(ctx, evtRow.RoutingKey, []byte(evtRow.Payload)); err != nil {
		t.Fatalf("dispatch release: %v", err)
	}
	if err := db.First(&dbRule, rule.ID).Error; err != nil {
		t.Fatalf("reload rule after release: %v", err)
	}
	if dbRule.ConsumedToday != 0 {
		t.Fatalf("投递闭环后 consumed_today = %d, want 0", dbRule.ConsumedToday)
	}

	// at-least-once 重复投递：预算不重复回补，台账仍只 1 行
	if err := promo.DispatchReleaseEvent(ctx, evtRow.RoutingKey, []byte(evtRow.Payload)); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if err := db.First(&dbRule, rule.ID).Error; err != nil {
		t.Fatalf("reload rule after redelivery: %v", err)
	}
	if dbRule.ConsumedToday != 0 {
		t.Fatalf("重复投递后 consumed_today = %d, want 0", dbRule.ConsumedToday)
	}
	var releases int64
	db.Model(&promo.PromoRelease{}).Where("order_id = ?", o.ID).Count(&releases)
	if releases != 1 {
		t.Fatalf("promo_release rows = %d, want 1", releases)
	}
}
