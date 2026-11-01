package promo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/repository/db/dao"
)

func TestRankPromoRulesScopeThresholdAndOrder(t *testing.T) {
	items := []CartItem{
		{ProductID: 1, CategoryID: 10, UnitCents: 6000, Quantity: 1},
		{ProductID: 2, CategoryID: 20, UnitCents: 5000, Quantity: 1},
	}
	all := &PromoRule{Model: dbmodel.Model{ID: 1}, RuleType: PromoRuleTypeAmount, Scope: PromoScopeAll, ThresholdCents: 10000, DiscountCents: 1000}
	cat := &PromoRule{Model: dbmodel.Model{ID: 2}, RuleType: PromoRuleTypeAmount, Scope: PromoScopeCategory, ScopeRefID: 10, ThresholdCents: 10000, DiscountCents: 3000}
	prod := &PromoRule{Model: dbmodel.Model{ID: 3}, RuleType: PromoRuleTypeDiscount, Scope: PromoScopeProduct, ScopeRefID: 2, ThresholdCents: 100, DiscountBps: 8001}
	tie := &PromoRule{Model: dbmodel.Model{ID: 4}, RuleType: PromoRuleTypeAmount, Scope: PromoScopeAll, ThresholdCents: 1, DiscountCents: 999}

	got := RankPromoRules(items, []*PromoRule{all, cat, prod, tie})
	// cat 只算类目 10 的 6000，不到门槛；prod 5000*(1-0.8001) 向下取整 = 999，与 tie 同额时保持入参顺序。
	want := []struct {
		id uint
		d  int64
	}{{1, 1000}, {3, 999}, {4, 999}}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Rule.ID != w.id || got[i].DiscountCents != w.d {
			t.Fatalf("rank[%d] = rule %d / %d, want rule %d / %d", i, got[i].Rule.ID, got[i].DiscountCents, w.id, w.d)
		}
	}
	if best, d := PickBestPromoRule(items, []*PromoRule{cat}); best != nil || d != 0 {
		t.Fatalf("below threshold must not match, got %v %d", best, d)
	}
}

func TestValidateRuleRejectsDangerousConfig(t *testing.T) {
	base := func() *PromoRuleCreateReq {
		return &PromoRuleCreateReq{
			Name: "r", RuleType: PromoRuleTypeAmount, Scope: PromoScopeAll,
			ThresholdCents: 10000, DiscountCents: 2000,
			StartAt: time.Now(), EndAt: time.Now().Add(time.Hour),
		}
	}
	if err := validateRule(base()); err != nil {
		t.Fatalf("valid rule rejected: %v", err)
	}
	cases := map[string]func(r *PromoRuleCreateReq){
		"满 1 分减 100 元":    func(r *PromoRuleCreateReq) { r.ThresholdCents, r.DiscountCents = 1, 10000 },
		"减额等于门槛":          func(r *PromoRuleCreateReq) { r.DiscountCents = r.ThresholdCents },
		"类目规则缺 ref":       func(r *PromoRuleCreateReq) { r.Scope = PromoScopeCategory },
		"商品规则缺 ref":       func(r *PromoRuleCreateReq) { r.Scope = PromoScopeProduct },
		"全场规则带 ref":       func(r *PromoRuleCreateReq) { r.ScopeRefID = 7 },
		"负预算":             func(r *PromoRuleCreateReq) { r.DailyBudgetCents = -1 },
		"折扣 bps 越界":       func(r *PromoRuleCreateReq) { r.RuleType, r.DiscountBps = PromoRuleTypeDiscount, 10000 },
		"end_at 早于 start": func(r *PromoRuleCreateReq) { r.EndAt = r.StartAt.Add(-time.Second) },
	}
	for name, mutate := range cases {
		r := base()
		mutate(r)
		if err := validateRule(r); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

// 新建是草稿、不参与计算；启用后才命中；生效后不能再改。
func TestRuleLifecycleDraftActivateUpdate(t *testing.T) {
	openPromoTestDB(t)
	ctx := context.Background()
	srv := GetPromoSrv()
	req := &PromoRuleCreateReq{
		Name: "满100减20", RuleType: PromoRuleTypeAmount, Scope: PromoScopeAll,
		ThresholdCents: 10000, DiscountCents: 2000,
		StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour),
	}
	r, err := srv.CreateRule(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != PromoStatusDraft {
		t.Fatalf("new rule status = %d, want draft", r.Status)
	}
	items := []CartItem{{ProductID: 1, UnitCents: 15000, Quantity: 1}}
	if resp, _ := srv.CalculateBestDiscount(ctx, items); resp.RuleID != 0 {
		t.Fatal("draft rule must not apply")
	}

	req.DiscountCents = 3000
	if _, err := srv.UpdateRule(ctx, r.ID, req); err != nil {
		t.Fatalf("update draft: %v", err)
	}
	if err := srv.ActivateRule(ctx, r.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if resp, _ := srv.CalculateBestDiscount(ctx, items); resp.RuleID != r.ID || resp.DiscountCents != 3000 {
		t.Fatalf("active rule should apply with updated amount, got %+v", resp)
	}
	if err := srv.ActivateRule(ctx, r.ID); !errors.Is(err, ErrPromoRuleNotDraft) {
		t.Fatalf("activate twice: %v", err)
	}
	if _, err := srv.UpdateRule(ctx, r.ID, req); !errors.Is(err, ErrPromoRuleNotDraft) {
		t.Fatalf("active rule must not be editable: %v", err)
	}
}

// 昨天花光的预算今天自动从 0 起算；试算剔除今天预算不够的规则并把次优留作降级候选。
func TestDailyBudgetResetsLazilyAndCalculateRespectsIt(t *testing.T) {
	db := openPromoTestDB(t)
	yesterday := BudgetDay(time.Now().AddDate(0, 0, -1))
	big := seedRule(t, db, &PromoRule{Name: "big", DiscountCents: 3000, DailyBudgetCents: 3000, ConsumedToday: 3000, BudgetDate: yesterday})
	small := seedRule(t, db, &PromoRule{Name: "small", DiscountCents: 1000})
	items := []CartItem{{ProductID: 1, UnitCents: 20000, Quantity: 1}}

	resp, err := GetPromoSrv().CalculateBestDiscount(context.Background(), items)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RuleID != big.ID || len(resp.Candidates) != 2 || resp.Candidates[1].Rule.ID != small.ID {
		t.Fatalf("yesterday's spend must not block today: %+v", resp)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return NewPromoDaoByDB(tx).AtomicConsumeBudget(tx, big.ID, 3000)
	}); err != nil {
		t.Fatalf("consume after day change: %v", err)
	}
	got := loadRule(t, db, big.ID)
	if got.ConsumedToday != 3000 || got.BudgetDate != BudgetDay(time.Now()) {
		t.Fatalf("budget not reset: consumed=%d date=%s", got.ConsumedToday, got.BudgetDate)
	}

	resp, _ = GetPromoSrv().CalculateBestDiscount(context.Background(), items)
	if resp.RuleID != small.ID || len(resp.Candidates) != 1 {
		t.Fatalf("exhausted rule must be skipped in calculate: %+v", resp)
	}
}

// 真并发扣预算：40 单抢 7000 分预算，每单 2000，只能成功 3 单。
func TestConcurrentConsumeNeverExceedsBudget(t *testing.T) {
	db := openPromoTestDB(t)
	r := seedRule(t, db, &PromoRule{Name: "hot", DiscountCents: 2000, DailyBudgetCents: 7000})

	var ok, exhausted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(orderID uint) {
			defer wg.Done()
			<-start
			err := db.Transaction(func(tx *gorm.DB) error {
				return GetPromoSrv().ApplyDiscountInTx(tx, orderID, r.ID, 2000)
			})
			switch {
			case err == nil:
				atomic.AddInt64(&ok, 1)
			case errors.Is(err, ErrPromoBudgetExhausted):
				atomic.AddInt64(&exhausted, 1)
			default:
				t.Errorf("order %d: %v", orderID, err)
			}
		}(uint(i + 1))
	}
	close(start)
	wg.Wait()

	if ok != 3 || exhausted != 37 {
		t.Fatalf("ok=%d exhausted=%d, want 3/37", ok, exhausted)
	}
	if got := loadRule(t, db, r.ID); got.ConsumedToday != 6000 {
		t.Fatalf("consumed_today = %d, want 6000", got.ConsumedToday)
	}
	var applied int64
	db.Model(&outbox.OutboxEvent{}).Where("routing_key = ?", "promo.applied").Count(&applied)
	if applied != 3 {
		t.Fatalf("promo.applied events = %d, want 3", applied)
	}
}

// 同一订单的关单事件并发重复投递只退一次；跨天订单只记台账不回补今天的预算。
func TestReleaseDiscountIdempotentAndSkipsOtherDays(t *testing.T) {
	db := openPromoTestDB(t)
	today := BudgetDay(time.Now())
	r := seedRule(t, db, &PromoRule{Name: "r", DiscountCents: 2000, DailyBudgetCents: 10000, ConsumedToday: 6000, BudgetDate: today})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := GetPromoSrv().ReleaseDiscount(context.Background(), 101, r.ID, 2000, today, "cancel"); err != nil {
				t.Errorf("release: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := loadRule(t, db, r.ID); got.ConsumedToday != 4000 {
		t.Fatalf("duplicate release restored twice: consumed=%d", got.ConsumedToday)
	}

	yesterday := BudgetDay(time.Now().AddDate(0, 0, -1))
	if err := GetPromoSrv().ReleaseDiscount(context.Background(), 102, r.ID, 2000, yesterday, "refund"); err != nil {
		t.Fatal(err)
	}
	if got := loadRule(t, db, r.ID); got.ConsumedToday != 4000 {
		t.Fatalf("yesterday's order must not refill today's budget: consumed=%d", got.ConsumedToday)
	}

	var recs []PromoRelease
	db.Order("order_id").Find(&recs)
	if len(recs) != 2 || !recs[0].Restored || recs[1].Restored {
		t.Fatalf("ledger restored flags wrong: %+v", recs)
	}
	var released int64
	db.Model(&outbox.OutboxEvent{}).Where("routing_key = ?", "promo.released").Count(&released)
	if released != 2 {
		t.Fatalf("promo.released events = %d, want 2", released)
	}
}

func seedRule(t *testing.T, db *gorm.DB, r *PromoRule) *PromoRule {
	t.Helper()
	r.RuleType = PromoRuleTypeAmount
	r.Scope = PromoScopeAll
	if r.ThresholdCents == 0 {
		r.ThresholdCents = 10000
	}
	r.StartAt = time.Now().Add(-time.Hour)
	r.EndAt = time.Now().Add(time.Hour)
	r.Status = PromoStatusActive
	if err := db.Create(r).Error; err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	return r
}

func loadRule(t *testing.T, db *gorm.DB, id uint) *PromoRule {
	t.Helper()
	var r PromoRule
	if err := db.First(&r, id).Error; err != nil {
		t.Fatal(err)
	}
	return &r
}

func openPromoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:promo-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// SQLite 只有库级写锁、没有行锁：单连接让并发 goroutine 排队进事务，
	// 证明的是条件 UPDATE / 唯一索引在并发调用下的守卫逻辑；行锁本身要在 MySQL 上压测。
	sqlDB.SetMaxOpenConns(1)
	previous := dao.SetTestDB(db)
	t.Cleanup(func() {
		dao.SetTestDB(previous)
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&PromoRule{}, &PromoRelease{}, &outbox.OutboxEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// 关单 / 退款传进来的 CreatedAt 是从 DB 读出的 UTC 时间：北京时间 0–8 点下的单也必须算成扣预算的那一天并回补。
func TestReleaseDiscountRestoresForUTCCreatedAtInEarlyLocalHours(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	prev := time.Local
	time.Local = sh
	t.Cleanup(func() { time.Local = prev })

	// 北京时间 2026-11-02 01:30 下单，DB 读回来是 UTC 的 2026-11-01 17:30。
	createdUTC := time.Date(2026, 11, 1, 17, 30, 0, 0, time.UTC)
	if got := BudgetDay(createdUTC); got != "2026-11-02" {
		t.Fatalf("BudgetDay(utc) = %s, want 2026-11-02", got)
	}

	db := openPromoTestDB(t)
	r := seedRule(t, db, &PromoRule{Name: "r", DiscountCents: 2000, DailyBudgetCents: 10000, ConsumedToday: 2000, BudgetDate: "2026-11-02"})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := GetPromoSrv().ReleaseDiscount(context.Background(), 201, r.ID, 2000, BudgetDay(createdUTC), "cancel"); err != nil {
				t.Errorf("release: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := loadRule(t, db, r.ID); got.ConsumedToday != 0 {
		t.Fatalf("budget not restored for early-hour order: consumed=%d", got.ConsumedToday)
	}
	var rec PromoRelease
	if err := db.Where("order_id = ?", 201).First(&rec).Error; err != nil || !rec.Restored {
		t.Fatalf("ledger restored = %+v, err=%v", rec, err)
	}
}
