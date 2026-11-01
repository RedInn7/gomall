package order_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/coupon"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/service/events"
)

// 排队下单与直接下单同一口径：consumer 落库时同样算满减、扣预算、核销券。
func TestAsyncOrderTaskAppliesPromoAndCoupon(t *testing.T) {
	db := openOrderTestDB(t)
	stubStockRedis(t, map[string]int64{})
	snowflake.InitSnowflake(1)
	if err := db.AutoMigrate(&product.Product{}, &promo.PromoRule{}, &coupon.CouponBatch{}, &coupon.UserCoupon{}); err != nil {
		t.Fatal(err)
	}
	p := &product.Product{Name: "mug", CategoryID: 1, Price: "100.00", Num: 5, OnSale: true, BossID: 22}
	if err := db.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	rule := &promo.PromoRule{Name: "满50减20", RuleType: promo.PromoRuleTypeAmount, Scope: promo.PromoScopeAll,
		ThresholdCents: 5000, DiscountCents: 2000, DailyBudgetCents: 100000,
		StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour), Status: promo.PromoStatusActive}
	if err := db.Create(rule).Error; err != nil {
		t.Fatal(err)
	}
	batch := &coupon.CouponBatch{Name: "c", Type: coupon.CouponTypeDiscount, Amount: 90, Total: 5, PerUser: 1, ValidDays: 7,
		StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}
	if err := db.Create(batch).Error; err != nil {
		t.Fatal(err)
	}
	uc, err := coupon.NewCouponDao(context.Background()).PersistClaim(11, batch.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	tickets := memTicketStore{}
	prev := order.AsyncOrderTicketStoreInstance()
	order.SetAsyncOrderTicketStore(tickets)
	t.Cleanup(func() { order.SetAsyncOrderTicketStore(prev) })

	body, _ := json.Marshal(order.AsyncOrderTask{Ticket: "777", UserID: 11, ProductID: p.ID, Num: 1, UserCouponID: uc.ID})
	if err := order.HandleAsyncOrderTask(context.Background(), body); err != nil {
		t.Fatal(err)
	}

	var o order.Order
	if err := db.Where("ticket = ?", "777").First(&o).Error; err != nil {
		t.Fatal(err)
	}
	// 10000 先减 2000 = 8000，再打 9 折减 800 = 7200。
	if o.PromoRuleID != rule.ID || o.PromoDiscountCents != 2000 || o.UserCouponID != uc.ID ||
		o.CouponDiscountCents != 800 || o.FinalCents != 7200 {
		t.Fatalf("async order discounts wrong: %+v", o)
	}
	var r promo.PromoRule
	db.First(&r, rule.ID)
	if r.ConsumedToday != 2000 {
		t.Fatalf("async order did not consume promo budget: %d", r.ConsumedToday)
	}
}

// 关单在同一事务里退回本单核销的券；order.cancelled 带上扣预算的那一天。
func TestCancelUnpaidOrderReleasesCouponAndCarriesBudgetDay(t *testing.T) {
	db := openOrderTestDB(t)
	stubStockRedis(t, map[string]int64{cache.StockAvailableKey(1): 0, cache.StockReservedKey(1): 1})
	if err := db.Exec("CREATE TABLE xcash_payment_intent (id INTEGER PRIMARY KEY, order_id INTEGER, status TEXT, expires_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&coupon.CouponBatch{}, &coupon.UserCoupon{}); err != nil {
		t.Fatal(err)
	}
	batch := &coupon.CouponBatch{Name: "c", Type: coupon.CouponTypeAmount, Amount: 100, Total: 5, PerUser: 1, ValidDays: 7,
		StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}
	if err := db.Create(batch).Error; err != nil {
		t.Fatal(err)
	}
	cdao := coupon.NewCouponDao(context.Background())
	uc, err := cdao.PersistClaim(11, batch.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-20 * time.Minute)
	o := seedOrder(t, db, &order.Order{Model: dbmodel.Model{ID: 401, CreatedAt: created}, UserID: 11, BossID: 22,
		Type: consts.OrderWaitPay, OrderNum: 401, PromoRuleID: 9, PromoDiscountCents: 20, UserCouponID: uc.ID,
		CouponDiscountCents: 100, FinalCents: -1})
	if err := cdao.UseInTx(uc.ID, 11, o.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := order.CancelUnpaidOrder(o.OrderNum); err != nil {
		t.Fatal(err)
	}
	assertOrderType(t, db, o.ID, consts.OrderClosed)
	var c coupon.UserCoupon
	db.First(&c, uc.ID)
	if c.Status != coupon.UserCouponStatusUnused || c.UsedOrderId != 0 {
		t.Fatalf("coupon not released on cancel: %+v", c)
	}
	var row outbox.OutboxEvent
	if err := db.Where("routing_key = ? AND aggregate_id = ?", "order.cancelled", o.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var evt events.OrderCancelled
	_ = json.Unmarshal([]byte(row.Payload), &evt)
	if evt.PromoBudgetDate != promo.BudgetDay(created) {
		t.Fatalf("event budget day = %q, want %q", evt.PromoBudgetDate, promo.BudgetDay(created))
	}
}
