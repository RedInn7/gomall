package promo

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/internal/shared/outbox"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

func initPromoLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

func setupSQLiteForPromo(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:promo-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&PromoRule{}, &PromoRelease{}, &outbox.OutboxEvent{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

func seedConsumedRule(t *testing.T, db *gorm.DB, consumed int64) *PromoRule {
	t.Helper()
	now := time.Now()
	r := &PromoRule{
		Name:             "release 满 80 减 10",
		RuleType:         PromoRuleTypeAmount,
		Scope:            PromoScopeAll,
		ThresholdCents:   8000,
		DiscountCents:    1000,
		DailyBudgetCents: 5000,
		ConsumedToday:    consumed,
		StartAt:          now.Add(-time.Hour),
		EndAt:            now.Add(time.Hour),
		Status:           PromoStatusActive,
	}
	if err := db.Create(r).Error; err != nil {
		t.Fatalf("create rule: %v", err)
	}
	return r
}

// TestPromo_ReleaseDiscount_Idempotent at-least-once 投递下同一订单重复释放：
// 预算只回补一次、promo_release 台账仅 1 行、promo.released 事件仅 1 条。
func TestPromo_ReleaseDiscount_Idempotent(t *testing.T) {
	initPromoLogForTest()
	db, restore := setupSQLiteForPromo(t)
	defer restore()

	rule := seedConsumedRule(t, db, 1000)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := GetPromoSrv().ReleaseDiscount(ctx, 42, rule.ID, 1000, "cancel"); err != nil {
			t.Fatalf("release #%d: %v", i+1, err)
		}
	}

	var dbRule PromoRule
	if err := db.First(&dbRule, rule.ID).Error; err != nil {
		t.Fatalf("reload rule: %v", err)
	}
	if dbRule.ConsumedToday != 0 {
		t.Fatalf("consumed_today = %d, want 0（预算只能回补一次）", dbRule.ConsumedToday)
	}

	var releases int64
	db.Model(&PromoRelease{}).Where("order_id = ?", 42).Count(&releases)
	if releases != 1 {
		t.Fatalf("promo_release rows = %d, want 1", releases)
	}

	var released int64
	db.Model(&outbox.OutboxEvent{}).
		Where("routing_key = ? AND aggregate_id = ?", "promo.released", 42).Count(&released)
	if released != 1 {
		t.Fatalf("promo.released outbox rows = %d, want 1", released)
	}
}

// TestPromo_ReleaseDiscount_DifferentOrdersIndependent 不同订单各自释放互不影响。
func TestPromo_ReleaseDiscount_DifferentOrdersIndependent(t *testing.T) {
	initPromoLogForTest()
	db, restore := setupSQLiteForPromo(t)
	defer restore()

	rule := seedConsumedRule(t, db, 2000)
	ctx := context.Background()

	if err := GetPromoSrv().ReleaseDiscount(ctx, 100, rule.ID, 1000, "cancel"); err != nil {
		t.Fatalf("release order 100: %v", err)
	}
	if err := GetPromoSrv().ReleaseDiscount(ctx, 101, rule.ID, 1000, "refund"); err != nil {
		t.Fatalf("release order 101: %v", err)
	}

	var dbRule PromoRule
	if err := db.First(&dbRule, rule.ID).Error; err != nil {
		t.Fatalf("reload rule: %v", err)
	}
	if dbRule.ConsumedToday != 0 {
		t.Fatalf("consumed_today = %d, want 0", dbRule.ConsumedToday)
	}
	var releases int64
	db.Model(&PromoRelease{}).Count(&releases)
	if releases != 2 {
		t.Fatalf("promo_release rows = %d, want 2", releases)
	}
}

// TestPromo_DispatchReleaseEvent_Parsing 不连 RMQ 验证消费入口的解析 / 分发：
//   - 非法 JSON / 未知 routing key → 报错（消费循环按毒消息 Nack 不重排）
//   - 未命中满减的事件（rule_id=0 / discount<=0）→ 直接放行，不触达 DB
func TestPromo_DispatchReleaseEvent_Parsing(t *testing.T) {
	initPromoLogForTest()
	ctx := context.Background()

	if err := DispatchReleaseEvent(ctx, "order.cancelled", []byte("not-json")); err == nil {
		t.Fatal("非法 JSON 应该报错")
	}
	if err := DispatchReleaseEvent(ctx, "order.paid", []byte("{}")); err == nil {
		t.Fatal("未知 routing key 应该报错")
	}

	// rule_id=0 / discount=0：无 DB 也应直接放行（不触达存储层）
	skip, _ := json.Marshal(events.OrderCancelled{OrderID: 7})
	if err := DispatchReleaseEvent(ctx, "order.cancelled", skip); err != nil {
		t.Fatalf("无满减字段的事件应跳过, got %v", err)
	}
	skipRefund, _ := json.Marshal(events.OrderRefundedEvent{OrderID: 8})
	if err := DispatchReleaseEvent(ctx, "order.refunded", skipRefund); err != nil {
		t.Fatalf("无满减字段的退款事件应跳过, got %v", err)
	}
}

// TestPromo_DispatchReleaseEvent_RefundedReleasesBudget order.refunded 路径全链路：
// payload 解析 → 分发 → 预算回补 + 台账落地。
func TestPromo_DispatchReleaseEvent_RefundedReleasesBudget(t *testing.T) {
	initPromoLogForTest()
	db, restore := setupSQLiteForPromo(t)
	defer restore()

	rule := seedConsumedRule(t, db, 1000)
	payload, _ := json.Marshal(events.OrderRefundedEvent{
		OrderID:            55,
		OrderNum:           920055,
		UserID:             1,
		Amount:             9000,
		PromoRuleID:        rule.ID,
		PromoDiscountCents: 1000,
	})

	if err := DispatchReleaseEvent(context.Background(), "order.refunded", payload); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var dbRule PromoRule
	if err := db.First(&dbRule, rule.ID).Error; err != nil {
		t.Fatalf("reload rule: %v", err)
	}
	if dbRule.ConsumedToday != 0 {
		t.Fatalf("consumed_today = %d, want 0", dbRule.ConsumedToday)
	}
	var rel PromoRelease
	if err := db.Where("order_id = ?", 55).First(&rel).Error; err != nil {
		t.Fatalf("load promo_release: %v", err)
	}
	if rel.RuleID != rule.ID || rel.DiscountCents != 1000 || rel.Reason != "refund" {
		t.Fatalf("promo_release 字段不符: %+v", rel)
	}
}
