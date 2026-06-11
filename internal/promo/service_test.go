package promo

import (
	"testing"
	"time"
)

// makeRule 构造一个 active 状态的规则。便于 table-driven 测试简写。
func makeRule(id uint, name string, ruleType, scope int, refID int64,
	thresholdCents, discountCents int64, discountBps int) *PromoRule {
	r := &PromoRule{
		Name:           name,
		RuleType:       ruleType,
		Scope:          scope,
		ScopeRefID:     refID,
		ThresholdCents: thresholdCents,
		DiscountCents:  discountCents,
		DiscountBps:    discountBps,
		StartAt:        time.Now().Add(-1 * time.Hour),
		EndAt:          time.Now().Add(24 * time.Hour),
		Status:         PromoStatusActive,
	}
	r.ID = id
	return r
}

// TestPromo_StepThresholdPicksHighestDiscount 验证阶梯门槛：
// 满 100 减 10 与 满 200 减 25 同时适用时，购物车 250 元应该选 25 元那条。
func TestPromo_StepThresholdPicksHighestDiscount(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 25000, Quantity: 1}, // 250 元
	}
	rules := []*PromoRule{
		makeRule(1, "满 100 减 10", PromoRuleTypeAmount, PromoScopeAll, 0, 10000, 1000, 0),
		makeRule(2, "满 200 减 25", PromoRuleTypeAmount, PromoScopeAll, 0, 20000, 2500, 0),
		makeRule(3, "满 300 减 40", PromoRuleTypeAmount, PromoScopeAll, 0, 30000, 4000, 0),
	}
	best, discount := PickBestPromoRule(items, rules)
	if best == nil || best.ID != 2 {
		t.Fatalf("expected rule id 2 (满 200 减 25), got %v", best)
	}
	if discount != 2500 {
		t.Fatalf("expected discount 2500 cents, got %d", discount)
	}
}

// TestPromo_DiscountVsAmount 满折扣与满减同时适用时选实际减得多的。
// 购物车 500 元：满 200 打 9 折 = 减 50 元；满 200 减 25 = 减 25 元 → 选打折。
func TestPromo_DiscountVsAmount(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 50000, Quantity: 1},
	}
	rules := []*PromoRule{
		makeRule(1, "满 200 减 25", PromoRuleTypeAmount, PromoScopeAll, 0, 20000, 2500, 0),
		makeRule(2, "满 200 9 折", PromoRuleTypeDiscount, PromoScopeAll, 0, 20000, 0, 9000),
	}
	best, discount := PickBestPromoRule(items, rules)
	if best == nil || best.ID != 2 {
		t.Fatalf("expected rule id 2 (9 折), got %v", best)
	}
	if discount != 5000 {
		t.Fatalf("expected discount 5000 cents (500 * 10%%), got %d", discount)
	}
}

// TestPromo_DiscountWinsOverAmountReverse 反向：购物车小，满减 25 比 9 折更多。
// 购物车 200 元：满 200 打 9 折 = 减 20 元；满 200 减 25 = 减 25 元 → 选满减。
func TestPromo_DiscountWinsOverAmountReverse(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 20000, Quantity: 1},
	}
	rules := []*PromoRule{
		makeRule(1, "满 200 减 25", PromoRuleTypeAmount, PromoScopeAll, 0, 20000, 2500, 0),
		makeRule(2, "满 200 9 折", PromoRuleTypeDiscount, PromoScopeAll, 0, 20000, 0, 9000),
	}
	best, discount := PickBestPromoRule(items, rules)
	if best == nil || best.ID != 1 {
		t.Fatalf("expected rule id 1 (满 200 减 25), got %v", best)
	}
	if discount != 2500 {
		t.Fatalf("expected discount 2500 cents, got %d", discount)
	}
}

// TestPromo_ThresholdNotMet 购物车 80 元 → 任何"满 100"规则都不该适用。
func TestPromo_ThresholdNotMet(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 8000, Quantity: 1},
	}
	rules := []*PromoRule{
		makeRule(1, "满 100 减 10", PromoRuleTypeAmount, PromoScopeAll, 0, 10000, 1000, 0),
	}
	best, discount := PickBestPromoRule(items, rules)
	if best != nil || discount != 0 {
		t.Fatalf("expected no rule, got best=%v discount=%d", best, discount)
	}
}

// TestPromo_CategoryScopeOnlyAppliesToCategory 类目级规则只在该类目子集合上判断 / 减免。
func TestPromo_CategoryScopeOnlyAppliesToCategory(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 12000, Quantity: 1}, // 类目 10 = 120 元
		{ProductID: 2, CategoryID: 20, UnitCents: 30000, Quantity: 1}, // 类目 20 = 300 元
	}
	rules := []*PromoRule{
		// 类目 10 满 100 减 15；该类目小计 120，命中 → 减 15
		makeRule(1, "图书满 100 减 15", PromoRuleTypeAmount, PromoScopeCategory, 10, 10000, 1500, 0),
		// 类目 20 满 500 减 60；该类目小计 300，不达门槛
		makeRule(2, "数码满 500 减 60", PromoRuleTypeAmount, PromoScopeCategory, 20, 50000, 6000, 0),
	}
	best, discount := PickBestPromoRule(items, rules)
	if best == nil || best.ID != 1 {
		t.Fatalf("expected rule 1, got %v", best)
	}
	if discount != 1500 {
		t.Fatalf("expected 1500 cents discount, got %d", discount)
	}
}

// TestPromo_ProductScopeOnlyAppliesToProduct 商品级仅对该商品行有效。
func TestPromo_ProductScopeOnlyAppliesToProduct(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 25000, Quantity: 1},
		{ProductID: 2, CategoryID: 10, UnitCents: 5000, Quantity: 1},
	}
	rules := []*PromoRule{
		makeRule(1, "P1 满 200 减 30", PromoRuleTypeAmount, PromoScopeProduct, 1, 20000, 3000, 0),
	}
	best, discount := PickBestPromoRule(items, rules)
	if best == nil || best.ID != 1 {
		t.Fatalf("expected rule 1, got %v", best)
	}
	if discount != 3000 {
		t.Fatalf("expected 3000 cents discount, got %d", discount)
	}
}

// TestPromo_SubtotalCents 简单合计校验。
func TestPromo_SubtotalCents(t *testing.T) {
	got := SubtotalCents([]CartItem{
		{UnitCents: 1000, Quantity: 3},
		{UnitCents: 500, Quantity: 2},
	})
	if got != 4000 {
		t.Fatalf("subtotal want 4000, got %d", got)
	}
}

// TestPromo_DiscountCapsAtBase 满减金额不应大于子集合计。
// 边界：低价小满减规则配置错（减 200 但门槛只 100）时，封顶在 base。
func TestPromo_DiscountCapsAtBase(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 12000, Quantity: 1},
	}
	rules := []*PromoRule{
		makeRule(1, "异常 满 100 减 200", PromoRuleTypeAmount, PromoScopeAll, 0, 10000, 20000, 0),
	}
	_, discount := PickBestPromoRule(items, rules)
	if discount != 12000 {
		t.Fatalf("expected capped 12000, got %d", discount)
	}
}

// TestPromo_NoApplicableRule 没有规则适用时 best == nil。
func TestPromo_NoApplicableRule(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 5000, Quantity: 1},
	}
	rules := []*PromoRule{
		makeRule(1, "数码满 500 减 60", PromoRuleTypeAmount, PromoScopeCategory, 20, 50000, 6000, 0),
	}
	best, _ := PickBestPromoRule(items, rules)
	if best != nil {
		t.Fatalf("expected nil, got %v", best)
	}
}
