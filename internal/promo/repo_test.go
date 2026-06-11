package promo

import (
	"context"
	"errors"
	"testing"
	"time"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// TestPromo_AtomicConsumeBudget_Exhaust 验证 DailyBudget 被打满时返回 ErrPromoBudgetExhausted。
// 同时复用 outbox 的 skip-if-no-mysql 套路：本地无 MySQL 时跳过。
func TestPromo_AtomicConsumeBudget_Exhaust(t *testing.T) {
	initPromoTestDB(t)
	if dao.NewDBClient(context.Background()) == nil {
		t.Skip("MySQL not initialized")
	}
	ctx := context.Background()
	d := NewPromoDao(ctx)

	now := time.Now()
	rule := &PromoRule{
		Name:             "test 满 100 减 10",
		RuleType:         PromoRuleTypeAmount,
		Scope:            PromoScopeAll,
		ThresholdCents:   10000,
		DiscountCents:    1000,
		DailyBudgetCents: 1500, // 一共 15 元预算
		StartAt:          now.Add(-time.Hour),
		EndAt:            now.Add(time.Hour),
		Status:           PromoStatusActive,
	}
	if err := d.Create(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	t.Cleanup(func() { d.DB.Unscoped().Delete(rule) })

	// 第一笔 10 元，应成功
	if err := d.AtomicConsumeBudget(d.DB, rule.ID, 1000); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	// 第二笔 10 元，剩余 5 元，应返回 ErrPromoBudgetExhausted
	err := d.AtomicConsumeBudget(d.DB, rule.ID, 1000)
	if !errors.Is(err, ErrPromoBudgetExhausted) {
		t.Fatalf("expected ErrPromoBudgetExhausted, got %v", err)
	}

	// 第三笔 5 元，刚好打满
	if err := d.AtomicConsumeBudget(d.DB, rule.ID, 500); err != nil {
		t.Fatalf("third consume: %v", err)
	}
	// 再来 1 元都不行
	err = d.AtomicConsumeBudget(d.DB, rule.ID, 1)
	if !errors.Is(err, ErrPromoBudgetExhausted) {
		t.Fatalf("expected exhausted on tail, got %v", err)
	}
}

// TestPromo_RestoreBudget 验证取消订单退还预算后可以重新被消费。
func TestPromo_RestoreBudget(t *testing.T) {
	initPromoTestDB(t)
	if dao.NewDBClient(context.Background()) == nil {
		t.Skip()
	}
	ctx := context.Background()
	d := NewPromoDao(ctx)

	now := time.Now()
	rule := &PromoRule{
		Name:             "test restore",
		RuleType:         PromoRuleTypeAmount,
		Scope:            PromoScopeAll,
		ThresholdCents:   10000,
		DiscountCents:    2000,
		DailyBudgetCents: 2000,
		StartAt:          now.Add(-time.Hour),
		EndAt:            now.Add(time.Hour),
		Status:           PromoStatusActive,
	}
	if err := d.Create(rule); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { d.DB.Unscoped().Delete(rule) })

	if err := d.AtomicConsumeBudget(d.DB, rule.ID, 2000); err != nil {
		t.Fatalf("consume: %v", err)
	}
	// 此时已打满
	if err := d.AtomicConsumeBudget(d.DB, rule.ID, 1); !errors.Is(err, ErrPromoBudgetExhausted) {
		t.Fatalf("expected exhausted, got %v", err)
	}
	// 退还
	if err := d.RestoreBudget(d.DB, rule.ID, 500); err != nil {
		t.Fatalf("restore: %v", err)
	}
	// 现在能再消费 500
	if err := d.AtomicConsumeBudget(d.DB, rule.ID, 500); err != nil {
		t.Fatalf("re-consume after restore: %v", err)
	}
	// 但 501 就不行
	if err := d.AtomicConsumeBudget(d.DB, rule.ID, 1); !errors.Is(err, ErrPromoBudgetExhausted) {
		t.Fatalf("expected exhausted on tail, got %v", err)
	}
}

// TestPromo_AtomicConsume_UnlimitedBudget DailyBudget=0 视为不限。
func TestPromo_AtomicConsume_UnlimitedBudget(t *testing.T) {
	initPromoTestDB(t)
	if dao.NewDBClient(context.Background()) == nil {
		t.Skip()
	}
	ctx := context.Background()
	d := NewPromoDao(ctx)

	now := time.Now()
	rule := &PromoRule{
		Name:             "test unlimited",
		RuleType:         PromoRuleTypeAmount,
		Scope:            PromoScopeAll,
		ThresholdCents:   10000,
		DiscountCents:    1000,
		DailyBudgetCents: 0,
		StartAt:          now.Add(-time.Hour),
		EndAt:            now.Add(time.Hour),
		Status:           PromoStatusActive,
	}
	if err := d.Create(rule); err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { d.DB.Unscoped().Delete(rule) })

	// 连续 5 次都应通过
	for i := 0; i < 5; i++ {
		if err := d.AtomicConsumeBudget(d.DB, rule.ID, 99999); err != nil {
			t.Fatalf("consume #%d: %v", i, err)
		}
	}
}

// TestPromo_ListActiveForCart 验证规则范围过滤：全场 + 命中类目 / 商品 都拉到，
// 不相关的不拉。
func TestPromo_ListActiveForCart(t *testing.T) {
	initPromoTestDB(t)
	if dao.NewDBClient(context.Background()) == nil {
		t.Skip()
	}
	ctx := context.Background()
	d := NewPromoDao(ctx)

	now := time.Now()
	rules := []*PromoRule{
		{Name: "全场 100-10", RuleType: 1, Scope: PromoScopeAll,
			ThresholdCents: 10000, DiscountCents: 1000,
			StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
			Status: PromoStatusActive},
		{Name: "图书 100-15", RuleType: 1, Scope: PromoScopeCategory, ScopeRefID: 10,
			ThresholdCents: 10000, DiscountCents: 1500,
			StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
			Status: PromoStatusActive},
		{Name: "数码 999-100", RuleType: 1, Scope: PromoScopeCategory, ScopeRefID: 20,
			ThresholdCents: 99900, DiscountCents: 10000,
			StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
			Status: PromoStatusActive},
		{Name: "已停 全场", RuleType: 1, Scope: PromoScopeAll,
			ThresholdCents: 10000, DiscountCents: 9999,
			StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
			Status: PromoStatusStopped},
	}
	for _, r := range rules {
		if err := d.Create(r); err != nil {
			t.Fatalf("create %s: %v", r.Name, err)
		}
	}
	t.Cleanup(func() {
		for _, r := range rules {
			d.DB.Unscoped().Delete(r)
		}
	})

	got, err := d.ListActiveForCart(now, []int64{10}, []int64{1})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// 期望命中：全场 + 图书 — 不该有数码、不该有 stopped。
	hit := map[string]bool{}
	for _, r := range got {
		hit[r.Name] = true
	}
	if !hit["全场 100-10"] || !hit["图书 100-15"] {
		t.Fatalf("missing expected hits, got %v", hit)
	}
	if hit["数码 999-100"] {
		t.Fatalf("数码 不应被拉到（不在 categoryIDs）")
	}
	if hit["已停 全场"] {
		t.Fatalf("stopped 规则不应被拉到")
	}
}

func initPromoTestDB(t *testing.T) {
	t.Helper()
	if dao.NewDBClient(context.Background()) != nil {
		return
	}
	re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
	conf.InitConfigForTest(&re)
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("MySQL not available: %v", r)
		}
	}()
	dao.InitMySQL()
	if db := dao.NewDBClient(context.Background()); db != nil {
		_ = db.AutoMigrate(&PromoRule{})
	}
}
