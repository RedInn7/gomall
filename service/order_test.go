package service

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
	"github.com/RedInn7/gomall/types"
)

// 这一组测试覆盖满减引擎接入下单链路后的 4 个关键行为：
//   1) 命中规则 → 订单金额按 final_cents 落库，PromoRuleID 写入
//   2) 无适用规则 → PromoRuleID=0，FinalCents = 商品总价
//   3) 多规则取最优 → 9 折 vs 满减 同时适用时选用户最划算那条
//   4) 预算耗尽 → 订单仍下单成功，PromoRuleID=0、FinalCents 恢复到无折扣价
//
// 依赖：sqlite in-memory（与 preorder_test 同套路）+ Redis DB 15 库存桶
// CGO 关 / Redis 不可用时整组 skip，CI 友好。

func setupSQLiteForOrder(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:order-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(
		&user.User{}, &model.Order{}, &model.Product{},
		&model.PromoRule{}, &model.OutboxEvent{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

func setupRedisForOrder(t *testing.T) func() {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 15})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis 127.0.0.1:6379 不可用：%v", err)
	}
	prev := cache.RedisClient
	cache.RedisClient = c
	return func() {
		c.FlushDB(context.Background())
		c.Close()
		cache.RedisClient = prev
	}
}

func ensureSnowflakeForOrder() {
	defer func() { _ = recover() }()
	snowflake.InitSnowflake(9)
}

// seedOrderProduct 建一个 BossID=1 / CategoryID=10 / 库存 50 的商品，并初始化 Redis 库存桶。
// 单价 / 名字交给调用方在 product 上额外设置。
func seedOrderProduct(t *testing.T, db *gorm.DB, name string, priceCents int64) *model.Product {
	t.Helper()
	p := &model.Product{
		Name:       name,
		CategoryID: 10,
		Num:        50,
		BossID:     1,
		Price:      "0", // 仅满足 not-null；引擎只看入参 unitCents
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := cache.InitStock(context.Background(), p.ID, int64(p.Num)); err != nil {
		t.Fatalf("init stock: %v", err)
	}
	_ = priceCents // 单价交给下单 req.Money 传入，这里仅占位避免被误删
	return p
}

func seedPromoRule(t *testing.T, db *gorm.DB, name string,
	ruleType, scope int, refID int64,
	thresholdCents, discountCents int64, discountBps int,
	dailyBudgetCents int64) *model.PromoRule {
	t.Helper()
	now := time.Now()
	r := &model.PromoRule{
		Name:             name,
		RuleType:         ruleType,
		Scope:            scope,
		ScopeRefID:       refID,
		ThresholdCents:   thresholdCents,
		DiscountCents:    discountCents,
		DiscountBps:      discountBps,
		DailyBudgetCents: dailyBudgetCents,
		StartAt:          now.Add(-time.Hour),
		EndAt:            now.Add(24 * time.Hour),
		Status:           model.PromoStatusActive,
	}
	if err := db.Create(r).Error; err != nil {
		t.Fatalf("create promo rule: %v", err)
	}
	return r
}

// TestOrderCreate_AppliesBestPromo 单品 100 元 + 全场满 80 减 10 → 订单金额 90 元，PromoRuleID 写入。
func TestOrderCreate_AppliesBestPromo(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForOrder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflakeForOrder()

	product := seedOrderProduct(t, db, "p-best-promo", 10000)
	rule := seedPromoRule(t, db, "满 80 减 10",
		model.PromoRuleTypeAmount, model.PromoScopeAll, 0,
		8000, 1000, 0, 0 /* unlimited budget */)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	resp, err := GetOrderSrv().OrderCreate(ctx, &types.OrderCreateReq{
		ProductID: product.ID,
		Num:       1,
		Money:     10000, // 单价 100 元
		AddressID: 1,
		BossID:    1,
	})
	if err != nil {
		t.Fatalf("OrderCreate: %v", err)
	}
	order, ok := resp.(*model.Order)
	if !ok {
		t.Fatalf("resp type %T", resp)
	}

	if order.PromoRuleID != rule.ID {
		t.Fatalf("PromoRuleID = %d, want %d", order.PromoRuleID, rule.ID)
	}
	if order.PromoDiscountCents != 1000 {
		t.Fatalf("PromoDiscountCents = %d, want 1000", order.PromoDiscountCents)
	}
	if order.FinalCents != 9000 {
		t.Fatalf("FinalCents = %d, want 9000", order.FinalCents)
	}

	// 预算消耗也应同步落库
	var dbRule model.PromoRule
	if err := db.First(&dbRule, rule.ID).Error; err != nil {
		t.Fatalf("reload rule: %v", err)
	}
	if dbRule.ConsumedToday != 1000 {
		t.Fatalf("ConsumedToday = %d, want 1000", dbRule.ConsumedToday)
	}

	// outbox 应该同时有 order.created 和 promo.applied
	var orderEvt, promoEvt int64
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "order.created", order.ID).Count(&orderEvt)
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "promo.applied", order.ID).Count(&promoEvt)
	if orderEvt != 1 || promoEvt != 1 {
		t.Fatalf("outbox rows: order.created=%d (want 1), promo.applied=%d (want 1)", orderEvt, promoEvt)
	}
}

// TestOrderCreate_NoApplicableRule 单品 100 元、唯一规则门槛 200 → 不命中，订单原价。
func TestOrderCreate_NoApplicableRule(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForOrder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflakeForOrder()

	product := seedOrderProduct(t, db, "p-no-rule", 10000)
	_ = seedPromoRule(t, db, "满 200 减 30",
		model.PromoRuleTypeAmount, model.PromoScopeAll, 0,
		20000, 3000, 0, 0)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 43})
	resp, err := GetOrderSrv().OrderCreate(ctx, &types.OrderCreateReq{
		ProductID: product.ID,
		Num:       1,
		Money:     10000,
		AddressID: 1,
		BossID:    1,
	})
	if err != nil {
		t.Fatalf("OrderCreate: %v", err)
	}
	order := resp.(*model.Order)

	if order.PromoRuleID != 0 {
		t.Fatalf("PromoRuleID = %d, want 0", order.PromoRuleID)
	}
	if order.PromoDiscountCents != 0 {
		t.Fatalf("PromoDiscountCents = %d, want 0", order.PromoDiscountCents)
	}
	if order.FinalCents != 10000 {
		t.Fatalf("FinalCents = %d, want 10000", order.FinalCents)
	}

	// 没命中规则就不应该落 promo.applied 事件
	var promoEvt int64
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=?", "promo.applied").Count(&promoEvt)
	if promoEvt != 0 {
		t.Fatalf("promo.applied outbox 不该出现，got %d", promoEvt)
	}
}

// TestOrderCreate_PicksBestAmongMultipleRules 多规则同时适用时取用户最划算那条。
// 购物车 500 元：满 200 减 25 (减 25) vs 满 200 打 9 折 (减 50) → 选 9 折。
// 这条等价于 "满减 + 后续叠加优惠券" 场景的"满减"前半段：满减结算后 final_cents 落库，
// 后续优惠券链路 (handler 层) 在 final_cents 上再减；本测试聚焦满减侧落库正确性。
func TestOrderCreate_PicksBestAmongMultipleRules(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForOrder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflakeForOrder()

	product := seedOrderProduct(t, db, "p-multi-rule", 50000)
	_ = seedPromoRule(t, db, "满 200 减 25",
		model.PromoRuleTypeAmount, model.PromoScopeAll, 0,
		20000, 2500, 0, 0)
	discountRule := seedPromoRule(t, db, "满 200 9 折",
		model.PromoRuleTypeDiscount, model.PromoScopeAll, 0,
		20000, 0, 9000, 0)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 44})
	resp, err := GetOrderSrv().OrderCreate(ctx, &types.OrderCreateReq{
		ProductID: product.ID,
		Num:       1,
		Money:     50000, // 单价 500 元
		AddressID: 1,
		BossID:    1,
	})
	if err != nil {
		t.Fatalf("OrderCreate: %v", err)
	}
	order := resp.(*model.Order)

	if order.PromoRuleID != discountRule.ID {
		t.Fatalf("PromoRuleID = %d, want %d (9 折)", order.PromoRuleID, discountRule.ID)
	}
	if order.PromoDiscountCents != 5000 {
		t.Fatalf("PromoDiscountCents = %d, want 5000 (500*10%%)", order.PromoDiscountCents)
	}
	if order.FinalCents != 45000 {
		t.Fatalf("FinalCents = %d, want 45000", order.FinalCents)
	}
}

// TestOrderCreate_BudgetExhaustedDowngrades 预算耗尽 → 订单仍创建成功、PromoRuleID=0、final 恢复到原价。
// 这是"宁可下游慢、不能上游 429"的真实落点：不能让满减抢预算导致下单失败。
func TestOrderCreate_BudgetExhaustedDowngrades(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForOrder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflakeForOrder()

	product := seedOrderProduct(t, db, "p-budget-exh", 10000)
	// 预算只剩 500 分，规则要扣 1000 分 → AtomicConsumeBudget 必返 ErrPromoBudgetExhausted
	rule := seedPromoRule(t, db, "满 80 减 10 (小预算)",
		model.PromoRuleTypeAmount, model.PromoScopeAll, 0,
		8000, 1000, 0, 1000 /* daily budget = 10 元 */)
	// 直接把 consumed_today 推到差 500 分就满
	if err := db.Model(&model.PromoRule{}).
		Where("id=?", rule.ID).
		Update("consumed_today", 500).Error; err != nil {
		t.Fatalf("seed consumed_today: %v", err)
	}

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 45})
	resp, err := GetOrderSrv().OrderCreate(ctx, &types.OrderCreateReq{
		ProductID: product.ID,
		Num:       1,
		Money:     10000,
		AddressID: 1,
		BossID:    1,
	})
	if err != nil {
		t.Fatalf("OrderCreate 应降级而非报错: %v", err)
	}
	order := resp.(*model.Order)

	if order.PromoRuleID != 0 {
		t.Fatalf("降级失败 PromoRuleID = %d, want 0", order.PromoRuleID)
	}
	if order.PromoDiscountCents != 0 {
		t.Fatalf("降级失败 PromoDiscountCents = %d, want 0", order.PromoDiscountCents)
	}
	if order.FinalCents != 10000 {
		t.Fatalf("降级失败 FinalCents = %d, want 10000", order.FinalCents)
	}

	// DB 二次校验：订单落库的字段也确实是无折扣
	var dbOrder model.Order
	if err := db.First(&dbOrder, order.ID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if dbOrder.PromoRuleID != 0 || dbOrder.PromoDiscountCents != 0 || dbOrder.FinalCents != 10000 {
		t.Fatalf("DB 上的满减字段未降级: %+v", dbOrder)
	}

	// 预算耗尽不应该写 promo.applied 事件
	var promoEvt int64
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "promo.applied", order.ID).Count(&promoEvt)
	if promoEvt != 0 {
		t.Fatalf("预算耗尽不应写 promo.applied，got %d", promoEvt)
	}

	// 规则的 consumed_today 也不该被推进（事务回滚 / 没扣到）
	var dbRule model.PromoRule
	if err := db.First(&dbRule, rule.ID).Error; err != nil {
		t.Fatalf("reload rule: %v", err)
	}
	if dbRule.ConsumedToday != 500 {
		t.Fatalf("预算耗尽场景 consumed_today 不应增长，want 500, got %d", dbRule.ConsumedToday)
	}
}
