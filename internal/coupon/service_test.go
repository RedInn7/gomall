package coupon

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/repository/cache"
)

// 30 个用户并发抢 10 张：Lua 闸门 + DB 守卫下恰好发出 10 张，Redis 与 DB 计数一致。
func TestConcurrentClaimNeverExceedsTotal(t *testing.T) {
	db := openCouponTestDB(t)
	mr := openCouponTestRedis(t)
	b := seedBatch(t, db, &CouponBatch{Total: 10, PerUser: 1})
	if err := cache.InitCouponStock(context.Background(), b.ID, b.Total, time.Hour); err != nil {
		t.Fatal(err)
	}

	var ok, soldOut int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for uid := uint(1); uid <= 30; uid++ {
		wg.Add(1)
		go func(uid uint) {
			defer wg.Done()
			<-start
			_, err := GetCouponSrv().Claim(asUser(uid), "", b.ID)
			switch {
			case err == nil:
				atomic.AddInt64(&ok, 1)
			case errors.Is(err, cache.ErrCouponSoldOut):
				atomic.AddInt64(&soldOut, 1)
			default:
				t.Errorf("user %d: %v", uid, err)
			}
		}(uid)
	}
	close(start)
	wg.Wait()

	if ok != 10 || soldOut != 20 {
		t.Fatalf("ok=%d soldOut=%d, want 10/20", ok, soldOut)
	}
	assertIssued(t, db, b.ID, 10)
	if v, _ := mr.Get(cache.CouponStockKey(b.ID)); v != "0" {
		t.Fatalf("redis stock = %s, want 0", v)
	}
}

// Redis 丢了库存 key：领券按 DB 剩余量重新装载，而不是对所有人报"已抢光"。
func TestClaimReloadsMissingStockKeyFromDB(t *testing.T) {
	db := openCouponTestDB(t)
	openCouponTestRedis(t)
	b := seedBatch(t, db, &CouponBatch{Total: 3, Claimed: 2, PerUser: 1})

	if _, err := GetCouponSrv().Claim(asUser(1), "", b.ID); err != nil {
		t.Fatalf("claim with missing key: %v", err)
	}
	if _, err := GetCouponSrv().Claim(asUser(2), "", b.ID); !errors.Is(err, cache.ErrCouponSoldOut) {
		t.Fatalf("reloaded stock must equal DB remaining, got %v", err)
	}
}

// 超出单人上限：Lua 闸门、DB 模式、落库守卫三条路径返回同一个错误。
func TestPerUserLimitErrorIsUnifiedAcrossPaths(t *testing.T) {
	db := openCouponTestDB(t)
	openCouponTestRedis(t)
	b := seedBatch(t, db, &CouponBatch{Total: 10, PerUser: 1})
	if err := cache.InitCouponStock(context.Background(), b.ID, b.Total, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := GetCouponSrv().Claim(asUser(1), "", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetCouponSrv().Claim(asUser(1), "", b.ID); !errors.Is(err, ErrPerUserLimitReached) {
		t.Fatalf("lua path: %v", err)
	}
	if _, err := NewCouponDao(context.Background()).ClaimWithDBLock(1, b.ID); !errors.Is(err, ErrPerUserLimitReached) {
		t.Fatalf("db mode: %v", err)
	}
	if _, err := NewCouponDao(context.Background()).PersistClaim(1, b.ID, 7); !errors.Is(err, ErrPerUserLimitReached) {
		t.Fatalf("persist guard: %v", err)
	}
}

// 回滚失败造成的幽灵消耗由对账抬回 DB 剩余量；Redis 已经不低于 DB 口径时不动。
func TestStockReconcileRaisesGhostConsumption(t *testing.T) {
	db := openCouponTestDB(t)
	mr := openCouponTestRedis(t)
	ghost := seedBatch(t, db, &CouponBatch{Total: 5, Claimed: 3})
	fine := seedBatch(t, db, &CouponBatch{Total: 5, Claimed: 1})
	mr.Set(cache.CouponStockKey(ghost.ID), "0")
	mr.Set(cache.CouponStockKey(fine.ID), "4")

	GetCouponSrv().RunStockReconcile()

	if v, _ := mr.Get(cache.CouponStockKey(ghost.ID)); v != "2" {
		t.Fatalf("ghost batch stock = %s, want 2", v)
	}
	if v, _ := mr.Get(cache.CouponStockKey(fine.ID)); v != "4" {
		t.Fatalf("healthy batch stock = %s, want 4", v)
	}
}

func TestValidateBatch(t *testing.T) {
	base := func() *CouponBatchCreateReq {
		return &CouponBatchCreateReq{Type: CouponTypeAmount, Threshold: 10000, Amount: 2000,
			StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
	}
	if err := validateBatch(base()); err != nil {
		t.Fatalf("valid batch rejected: %v", err)
	}
	bad := map[string]func(r *CouponBatchCreateReq){
		"折扣券 100": func(r *CouponBatchCreateReq) { r.Type, r.Amount = CouponTypeDiscount, 100 },
		"折扣券 0":   func(r *CouponBatchCreateReq) { r.Type, r.Amount = CouponTypeDiscount, 0 },
		"负门槛":     func(r *CouponBatchCreateReq) { r.Threshold = -1 },
		"满减券减额为负": func(r *CouponBatchCreateReq) { r.Amount = -5 },
		"减额超过门槛":  func(r *CouponBatchCreateReq) { r.Amount = 20000 },
		"结束早于开始":  func(r *CouponBatchCreateReq) { r.EndAt = r.StartAt },
	}
	for name, mutate := range bad {
		r := base()
		mutate(r)
		if err := validateBatch(r); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestComputeDiscount(t *testing.T) {
	amount := &CouponBatch{Type: CouponTypeAmount, Threshold: 5000, Amount: 1000}
	if d := ComputeDiscount(amount, 4999); d != 0 {
		t.Fatalf("below threshold: %d", d)
	}
	if d := ComputeDiscount(amount, 5000); d != 1000 {
		t.Fatalf("amount coupon: %d", d)
	}
	if d := ComputeDiscount(&CouponBatch{Type: CouponTypeAmount, Amount: 1000}, 300); d != 300 {
		t.Fatalf("no-threshold coupon must cap at base: %d", d)
	}
	if d := ComputeDiscount(&CouponBatch{Type: CouponTypeDiscount, Amount: 85}, 999); d != 149 {
		t.Fatalf("85 折 on 999 = %d, want 149 (floor)", d)
	}
}

// 同一张券被 10 笔订单并发核销只成功一次；关单退回后可再用，过期后退回直接记已过期。
func TestUseCouponOnceAndRelease(t *testing.T) {
	db := openCouponTestDB(t)
	b := seedBatch(t, db, &CouponBatch{Total: 10, PerUser: 2})
	uc, err := NewCouponDao(context.Background()).PersistClaim(1, b.ID, 7)
	if err != nil {
		t.Fatal(err)
	}

	var used int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for oid := uint(1); oid <= 10; oid++ {
		wg.Add(1)
		go func(oid uint) {
			defer wg.Done()
			<-start
			err := db.Transaction(func(tx *gorm.DB) error {
				return NewCouponDaoByDB(tx).UseInTx(uc.ID, 1, oid, time.Now())
			})
			if err == nil {
				atomic.AddInt64(&used, 1)
			} else if !errors.Is(err, ErrCouponUnavailable) {
				t.Errorf("order %d: %v", oid, err)
			}
		}(oid)
	}
	close(start)
	wg.Wait()
	if used != 1 {
		t.Fatalf("coupon used %d times, want 1", used)
	}
	got := loadCoupon(t, db, uc.ID)
	if got.Status != UserCouponStatusUsed || got.UsedOrderId == 0 || got.UsedAt == nil {
		t.Fatalf("used coupon fields not written: %+v", got)
	}
	if err := NewCouponDao(context.Background()).UseInTx(uc.ID, 2, 99, time.Now()); !errors.Is(err, ErrCouponUnavailable) {
		t.Fatalf("other user must not use it: %v", err)
	}

	// 别的订单退不动这张券；核销它的订单关单才退回。
	dao := NewCouponDao(context.Background())
	if err := dao.ReleaseForOrderInTx(uc.ID, got.UsedOrderId+100, time.Now()); err != nil {
		t.Fatal(err)
	}
	if loadCoupon(t, db, uc.ID).Status != UserCouponStatusUsed {
		t.Fatal("release by a different order must be no-op")
	}
	if err := dao.ReleaseForOrderInTx(uc.ID, got.UsedOrderId, time.Now()); err != nil {
		t.Fatal(err)
	}
	if c := loadCoupon(t, db, uc.ID); c.Status != UserCouponStatusUnused || c.UsedOrderId != 0 || c.UsedAt != nil {
		t.Fatalf("released coupon not reusable: %+v", c)
	}

	if err := dao.UseInTx(uc.ID, 1, 7, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := dao.ReleaseForOrderInTx(uc.ID, 7, uc.ExpireAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if c := loadCoupon(t, db, uc.ID); c.Status != UserCouponStatusExpired {
		t.Fatalf("coupon released after expiry should be expired, got %d", c.Status)
	}
}

// 已过期但过期任务还没跑的券不出现在"未使用"里；过期任务跑完状态落库。
func TestListMyCouponsAndExpireSweep(t *testing.T) {
	db := openCouponTestDB(t)
	b := seedBatch(t, db, &CouponBatch{Total: 10, PerUser: 5})
	dao := NewCouponDao(context.Background())
	live, _ := dao.PersistClaim(1, b.ID, 7)
	stale, _ := dao.PersistClaim(1, b.ID, 7)
	db.Model(stale).Update("expire_at", time.Now().Add(-time.Minute))

	unused, err := dao.ListUserCoupons(1, UserCouponStatusUnused, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(unused) != 1 || unused[0].ID != live.ID {
		t.Fatalf("unused list = %+v, want only live coupon", unused)
	}
	expired, _ := dao.ListUserCoupons(1, UserCouponStatusExpired, time.Now())
	if len(expired) != 1 || expired[0].ID != stale.ID {
		t.Fatalf("expired list = %+v, want stale coupon", expired)
	}

	GetCouponSrv().RunExpireCoupons()
	if c := loadCoupon(t, db, stale.ID); c.Status != UserCouponStatusExpired {
		t.Fatalf("expire sweep did not flip status: %d", c.Status)
	}
	if c := loadCoupon(t, db, live.ID); c.Status != UserCouponStatusUnused {
		t.Fatalf("live coupon flipped: %d", c.Status)
	}
}

func seedBatch(t *testing.T, db *gorm.DB, b *CouponBatch) *CouponBatch {
	t.Helper()
	now := time.Now()
	b.Name, b.Type, b.Amount = "b", CouponTypeAmount, 1000
	if b.PerUser == 0 {
		b.PerUser = 1
	}
	b.StartAt, b.EndAt, b.ValidDays = now.Add(-time.Hour), now.Add(time.Hour), 7
	if err := db.Create(b).Error; err != nil {
		t.Fatal(err)
	}
	return b
}

func loadCoupon(t *testing.T, db *gorm.DB, id uint) *UserCoupon {
	t.Helper()
	var c UserCoupon
	if err := db.First(&c, id).Error; err != nil {
		t.Fatal(err)
	}
	return &c
}

func assertIssued(t *testing.T, db *gorm.DB, batchID uint, want int64) {
	t.Helper()
	var issued int64
	db.Model(&UserCoupon{}).Where("batch_id = ?", batchID).Count(&issued)
	var b CouponBatch
	db.First(&b, batchID)
	if issued != want || b.Claimed != want {
		t.Fatalf("issued=%d claimed=%d, want %d", issued, b.Claimed, want)
	}
}

func asUser(id uint) context.Context {
	return ctl.NewContext(context.Background(), &ctl.UserInfo{Id: id})
}

func openCouponTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prev := cache.RedisClient
	cache.RedisClient = client
	t.Cleanup(func() {
		cache.RedisClient = prev
		_ = client.Close()
	})
	return mr
}
