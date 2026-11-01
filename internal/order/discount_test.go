package order

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/internal/coupon"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 试算时两条规则都还有预算，10 单并发下单：最优规则的预算只够 2 单，
// 其余 8 单降级到次优规则，而不是直接变成无折扣。
func TestConcurrentOrdersFallBackToNextPromoWhenBudgetExhausted(t *testing.T) {
	db := openDiscountTestDB(t)
	hot := seedPromoRule(t, db, "hot", 3000, 6000)
	cold := seedPromoRule(t, db, "cold", 1000, 0)
	srv := NewOrderSrv(promo.GetPromoSrv())

	quote, err := srv.quoteOrder(context.Background(), 11, 1, 0, 10000, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(quote.promos) != 2 || quote.promos[0].Rule.ID != hot.ID {
		t.Fatalf("quote candidates = %+v", quote.promos)
	}

	orders := runConcurrentOrders(t, db, srv, 10, func(int) *orderQuote { return quote })
	got := map[uint]int{}
	for _, o := range orders {
		if o == nil {
			t.Fatal("order failed")
		}
		got[o.PromoRuleID]++
		want := map[uint]int64{hot.ID: 7000, cold.ID: 9000}[o.PromoRuleID]
		if o.FinalCents != want {
			t.Fatalf("order rule=%d final=%d, want %d", o.PromoRuleID, o.FinalCents, want)
		}
		persisted := loadOrder(t, db, o.ID)
		if persisted.PromoRuleID != o.PromoRuleID || persisted.FinalCents != o.FinalCents {
			t.Fatalf("discount fields not persisted: %+v", persisted)
		}
	}
	if got[hot.ID] != 2 || got[cold.ID] != 8 {
		t.Fatalf("rule distribution = %v, want hot 2 / cold 8", got)
	}
	var r promo.PromoRule
	db.First(&r, hot.ID)
	if r.ConsumedToday != 6000 {
		t.Fatalf("hot consumed = %d, want 6000", r.ConsumedToday)
	}
}

// 先满减、后券：券在满减后的金额上算门槛和减免；同一张券两单并发下单只有一单核销成功。
func TestCouponAppliesAfterPromoAndIsUsedOnce(t *testing.T) {
	db := openDiscountTestDB(t)
	rule := seedPromoRule(t, db, "r", 2000, 0)
	uc := seedUserCoupon(t, db, 11, &coupon.CouponBatch{Type: coupon.CouponTypeAmount, Threshold: 8000, Amount: 1000})
	srv := NewOrderSrv(promo.GetPromoSrv())

	quote, err := srv.quoteOrder(context.Background(), 11, 1, 0, 10000, 1, uc.ID)
	if err != nil {
		t.Fatalf("quote with coupon: %v", err)
	}
	orders := runConcurrentOrders(t, db, srv, 2, func(int) *orderQuote { return quote })
	var ok *Order
	for _, o := range orders {
		if o != nil {
			if ok != nil {
				t.Fatal("one coupon used by two orders")
			}
			ok = o
		}
	}
	if ok == nil {
		t.Fatal("no order succeeded")
	}
	if ok.PromoRuleID != rule.ID || ok.PromoDiscountCents != 2000 || ok.UserCouponID != uc.ID ||
		ok.CouponDiscountCents != 1000 || ok.FinalCents != 7000 {
		t.Fatalf("order discounts wrong: %+v", ok)
	}
	if ok.PayableCents() != 7000 {
		t.Fatalf("payable = %d, want 7000", ok.PayableCents())
	}
	var c coupon.UserCoupon
	db.First(&c, uc.ID)
	if c.Status != coupon.UserCouponStatusUsed || c.UsedOrderId != ok.ID {
		t.Fatalf("coupon not written off: %+v", c)
	}
	var orderCount int64
	db.Model(&Order{}).Count(&orderCount)
	if orderCount != 1 {
		t.Fatalf("losing order must roll back, orders=%d", orderCount)
	}
}

func TestQuoteRejectsUnusableCoupon(t *testing.T) {
	db := openDiscountTestDB(t)
	seedPromoRule(t, db, "r", 2000, 0)
	uc := seedUserCoupon(t, db, 11, &coupon.CouponBatch{Type: coupon.CouponTypeAmount, Threshold: 8000, Amount: 1000})
	srv := NewOrderSrv(promo.GetPromoSrv())

	// 原价 9000 满足券门槛，但先减 2000 满减后只剩 7000。
	if _, err := srv.quoteOrder(context.Background(), 11, 1, 0, 9000, 1, uc.ID); !errors.Is(err, coupon.ErrCouponThresholdNotMet) {
		t.Fatalf("threshold after promo: %v", err)
	}
	if _, err := srv.quoteOrder(context.Background(), 12, 1, 0, 20000, 1, uc.ID); !errors.Is(err, coupon.ErrCouponUnavailable) {
		t.Fatalf("other user's coupon: %v", err)
	}
	db.Model(&coupon.UserCoupon{}).Where("id = ?", uc.ID).Update("expire_at", time.Now().Add(-time.Second))
	if _, err := srv.quoteOrder(context.Background(), 11, 1, 0, 20000, 1, uc.ID); !errors.Is(err, coupon.ErrCouponUnavailable) {
		t.Fatalf("expired coupon: %v", err)
	}
}

func TestPayableCents(t *testing.T) {
	cases := []struct {
		o    Order
		want int64
	}{
		{Order{Money: 500, Num: 2}, 1000},                                  // 老订单没写 FinalCents
		{Order{Money: 500, Num: 2, PromoRuleID: 1, FinalCents: 0}, 0},      // 满减全额抵扣
		{Order{Money: 500, Num: 2, UserCouponID: 3, FinalCents: 0}, 0},     // 只用券全额抵扣
		{Order{Money: 500, Num: 2, UserCouponID: 3, FinalCents: 900}, 900}, // 只用券
		{Order{Money: 500, Num: 2, PromoRuleID: 1, FinalCents: 700}, 700},  // 只满减
	}
	for i, c := range cases {
		if got := c.o.PayableCents(); got != c.want {
			t.Errorf("case %d: got %d want %d", i, got, c.want)
		}
	}
}

func runConcurrentOrders(t *testing.T, db *gorm.DB, srv *OrderSrv, n int, quoteFor func(int) *orderQuote) []*Order {
	t.Helper()
	out := make([]*Order, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			q := quoteFor(i)
			o := &Order{UserID: 11, ProductID: 1, BossID: 22, AddressID: 1, Num: 1, Money: q.subtotal,
				OrderNum: uint64(70000 + i), FinalCents: q.subtotal}
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := NewOrderDaoByDB(tx).CreateOrder(o); err != nil {
					return err
				}
				return srv.applyDiscountsInTx(tx, o, q)
			})
			if err == nil {
				out[i] = o
			} else if !errors.Is(err, coupon.ErrCouponUnavailable) {
				t.Errorf("order %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}

func seedPromoRule(t *testing.T, db *gorm.DB, name string, discount, budget int64) *promo.PromoRule {
	t.Helper()
	r := &promo.PromoRule{Name: name, RuleType: promo.PromoRuleTypeAmount, Scope: promo.PromoScopeAll,
		ThresholdCents: 5000, DiscountCents: discount, DailyBudgetCents: budget,
		StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour), Status: promo.PromoStatusActive}
	if err := db.Create(r).Error; err != nil {
		t.Fatal(err)
	}
	return r
}

func seedUserCoupon(t *testing.T, db *gorm.DB, userID uint, b *coupon.CouponBatch) *coupon.UserCoupon {
	t.Helper()
	b.Name, b.Total, b.PerUser, b.ValidDays = "c", 10, 1, 7
	b.StartAt, b.EndAt = time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if err := db.Create(b).Error; err != nil {
		t.Fatal(err)
	}
	uc, err := coupon.NewCouponDao(context.Background()).PersistClaim(userID, b.ID, b.ValidDays)
	if err != nil {
		t.Fatal(err)
	}
	return uc
}

func loadOrder(t *testing.T, db *gorm.DB, id uint) *Order {
	t.Helper()
	var o Order
	if err := db.First(&o, id).Error; err != nil {
		t.Fatal(err)
	}
	return &o
}

func openDiscountTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:discount-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// SQLite 没有行锁：单连接让并发 goroutine 排队进事务，验证的是条件 UPDATE 守卫在并发调用下的结果。
	sqlDB.SetMaxOpenConns(1)
	previous, previousLogger := dao.SetTestDB(db), util.LogrusObj
	util.LogrusObj = logrus.New()
	t.Cleanup(func() {
		dao.SetTestDB(previous)
		util.LogrusObj = previousLogger
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&Order{}, &promo.PromoRule{}, &promo.PromoRelease{},
		&coupon.CouponBatch{}, &coupon.UserCoupon{}, &outbox.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	return db
}
