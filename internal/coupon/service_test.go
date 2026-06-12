package coupon

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 优惠券域的 DB 闭环测试：sqlite in-memory，覆盖批次创建校验、活动期过滤、
// DB 锁模式领券（成功 / 单人上限 / 抢光 / 不在活动期）以及我的券列表过滤。
//
// Claim 的 redis 模式（Lua 原子扣减 + 落库回滚）依赖 Redis，
// 其缓存侧逻辑由 repository/cache/coupon_test.go 覆盖，这里只走 mode="db" 路径。

func setupSQLiteForCoupon(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:coupon-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&CouponBatch{}, &UserCoupon{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() {
		dao.SetTestDB(prev)
	}
}

func seedCouponBatch(t *testing.T, db *gorm.DB, total, perUser int64, start, end time.Time) *CouponBatch {
	t.Helper()
	b := &CouponBatch{
		Name:      "batch-" + t.Name(),
		Type:      CouponTypeAmount,
		Threshold: 10000,
		Amount:    1000,
		Total:     total,
		PerUser:   perUser,
		StartAt:   start,
		EndAt:     end,
		ValidDays: 7,
	}
	if err := db.Create(b).Error; err != nil {
		t.Fatalf("create batch: %v", err)
	}
	return b
}

func TestCouponCreateBatch_RejectsInvalidWindow(t *testing.T) {
	initLogForTest()
	now := time.Now()
	// end_at 不晚于 start_at：参数校验先于 DB / Redis，任何资源都不应被触达
	_, err := GetCouponSrv().CreateBatch(context.Background(), &CouponBatchCreateReq{
		Name: "bad-window", Type: CouponTypeAmount, Amount: 500,
		Total: 10, PerUser: 1, ValidDays: 7,
		StartAt: now, EndAt: now,
	})
	if err == nil {
		t.Fatal("end_at == start_at 应当被拒")
	}
}

func TestCouponListActiveBatches_FiltersByWindow(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCoupon(t)
	defer cleanup()

	now := time.Now()
	active := seedCouponBatch(t, db, 100, 1, now.Add(-time.Hour), now.Add(time.Hour))
	seedCouponBatch(t, db, 100, 1, now.Add(-48*time.Hour), now.Add(-24*time.Hour)) // 已结束
	seedCouponBatch(t, db, 100, 1, now.Add(24*time.Hour), now.Add(48*time.Hour))   // 未开始

	resp, err := GetCouponSrv().ListActiveBatches(context.Background())
	if err != nil {
		t.Fatalf("ListActiveBatches: %v", err)
	}
	list, ok := resp.([]*CouponBatch)
	if !ok {
		t.Fatalf("resp type = %T", resp)
	}
	if len(list) != 1 || list[0].ID != active.ID {
		t.Fatalf("expect only active batch %d, got %+v", active.ID, list)
	}
}

func TestCouponClaim_DBLock_HappyPath(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCoupon(t)
	defer cleanup()

	now := time.Now()
	b := seedCouponBatch(t, db, 5, 2, now.Add(-time.Hour), now.Add(time.Hour))
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 301})

	resp, err := GetCouponSrv().Claim(ctx, "db", b.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	uc, ok := resp.(*UserCoupon)
	if !ok || uc == nil {
		t.Fatalf("resp type = %T", resp)
	}
	if uc.UserId != 301 || uc.BatchId != b.ID {
		t.Fatalf("user/batch = %d/%d", uc.UserId, uc.BatchId)
	}
	if uc.Status != UserCouponStatusUnused {
		t.Fatalf("status = %d, want unused", uc.Status)
	}
	if uc.Code == "" {
		t.Fatal("code 不应为空")
	}
	// 有效期 = 领取时间 + ValidDays
	wantExpire := uc.ClaimedAt.AddDate(0, 0, b.ValidDays)
	if !uc.ExpireAt.Equal(wantExpire) {
		t.Fatalf("expire_at = %v, want %v", uc.ExpireAt, wantExpire)
	}

	// DB 侧已领数同步 +1
	var got CouponBatch
	if err := db.First(&got, b.ID).Error; err != nil {
		t.Fatalf("reload batch: %v", err)
	}
	if got.Claimed != 1 {
		t.Fatalf("claimed = %d, want 1", got.Claimed)
	}
}

func TestCouponClaim_DBLock_PerUserLimit(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCoupon(t)
	defer cleanup()

	now := time.Now()
	b := seedCouponBatch(t, db, 100, 1, now.Add(-time.Hour), now.Add(time.Hour))
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 302})

	if _, err := GetCouponSrv().Claim(ctx, "db", b.ID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	_, err := GetCouponSrv().Claim(ctx, "db", b.ID)
	if err == nil {
		t.Fatal("超出单人上限应当被拒")
	}

	// 被拒的领取不应造成超发
	var got CouponBatch
	if err := db.First(&got, b.ID).Error; err != nil {
		t.Fatalf("reload batch: %v", err)
	}
	if got.Claimed != 1 {
		t.Fatalf("claimed = %d, want 1", got.Claimed)
	}
	var cnt int64
	db.Model(&UserCoupon{}).Where("user_id=? AND batch_id=?", 302, b.ID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("user coupon rows = %d, want 1", cnt)
	}
}

// TestCouponClaim_DBLock_SameSecondCodesUnique 同一用户同一批次在同一秒内连续领取：
// 券码带随机熵后两张都应落库成功且 code 互异，不再撞 code 唯一索引。
func TestCouponClaim_DBLock_SameSecondCodesUnique(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCoupon(t)
	defer cleanup()

	now := time.Now()
	b := seedCouponBatch(t, db, 10, 2, now.Add(-time.Hour), now.Add(time.Hour))
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 307})

	resp1, err := GetCouponSrv().Claim(ctx, "db", b.ID)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	resp2, err := GetCouponSrv().Claim(ctx, "db", b.ID)
	if err != nil {
		t.Fatalf("second claim in same second: %v", err)
	}

	uc1, uc2 := resp1.(*UserCoupon), resp2.(*UserCoupon)
	if uc1.Code == "" || uc2.Code == "" {
		t.Fatalf("code 不应为空: %q / %q", uc1.Code, uc2.Code)
	}
	if uc1.Code == uc2.Code {
		t.Fatalf("同秒两次领取的券码不应相同: %q", uc1.Code)
	}

	var cnt int64
	db.Model(&UserCoupon{}).Where("user_id=? AND batch_id=?", 307, b.ID).Count(&cnt)
	if cnt != 2 {
		t.Fatalf("user coupon rows = %d, want 2", cnt)
	}
}

func TestCouponClaim_DBLock_SoldOut(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCoupon(t)
	defer cleanup()

	now := time.Now()
	b := seedCouponBatch(t, db, 1, 5, now.Add(-time.Hour), now.Add(time.Hour))

	ctxA := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 303})
	if _, err := GetCouponSrv().Claim(ctxA, "db", b.ID); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	ctxB := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 304})
	_, err := GetCouponSrv().Claim(ctxB, "db", b.ID)
	if err == nil {
		t.Fatal("库存售罄应当被拒")
	}

	var got CouponBatch
	if err := db.First(&got, b.ID).Error; err != nil {
		t.Fatalf("reload batch: %v", err)
	}
	if got.Claimed != got.Total {
		t.Fatalf("claimed = %d, want %d", got.Claimed, got.Total)
	}
}

func TestCouponClaim_DBLock_OutsideWindow(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCoupon(t)
	defer cleanup()

	now := time.Now()
	expired := seedCouponBatch(t, db, 10, 1, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 305})

	_, err := GetCouponSrv().Claim(ctx, "db", expired.ID)
	if err == nil {
		t.Fatal("活动已结束应当被拒")
	}
	var cnt int64
	db.Model(&UserCoupon{}).Where("batch_id=?", expired.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("过期批次不应产生领取记录，got %d", cnt)
	}
}

func TestCouponListMyCoupons_StatusFilter(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCoupon(t)
	defer cleanup()

	const userID = uint(306)
	now := time.Now()
	rows := []*UserCoupon{
		{UserId: userID, BatchId: 1, Code: "c-306-1", Status: UserCouponStatusUnused,
			ClaimedAt: now, ExpireAt: now.AddDate(0, 0, 7)},
		{UserId: userID, BatchId: 2, Code: "c-306-2", Status: UserCouponStatusUsed,
			ClaimedAt: now, ExpireAt: now.AddDate(0, 0, 7)},
		{UserId: 999, BatchId: 1, Code: "c-999-1", Status: UserCouponStatusUnused,
			ClaimedAt: now, ExpireAt: now.AddDate(0, 0, 7)},
	}
	for _, r := range rows {
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("seed user coupon: %v", err)
		}
	}

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: userID})

	// status=0：本人全部
	resp, err := GetCouponSrv().ListMyCoupons(ctx, 0)
	if err != nil {
		t.Fatalf("ListMyCoupons(0): %v", err)
	}
	all := resp.([]*UserCoupon)
	if len(all) != 2 {
		t.Fatalf("expect 2 coupons, got %d", len(all))
	}

	// status=2：只剩已使用
	resp, err = GetCouponSrv().ListMyCoupons(ctx, UserCouponStatusUsed)
	if err != nil {
		t.Fatalf("ListMyCoupons(used): %v", err)
	}
	used := resp.([]*UserCoupon)
	if len(used) != 1 || used[0].Code != "c-306-2" {
		t.Fatalf("expect only used coupon c-306-2, got %+v", used)
	}
}

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}
