package coupon

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// db 模式只加 claimed、不扣 Redis 库存；之后走 Lua 的请求在 Redis 里仍能扣成功，
// 落库这一步必须以 DB 总量为准拦住。
func TestMixedClaimModesNeverExceedTotal(t *testing.T) {
	db := openCouponTestDB(t)
	now := time.Now()
	batch := &CouponBatch{
		Name: "mixed", Type: CouponTypeAmount, Amount: 100, Total: 3, PerUser: 1,
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ValidDays: 7,
	}
	if err := db.Create(batch).Error; err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	couponDao := NewCouponDao(context.Background())

	for uid := uint(1); uid <= 2; uid++ {
		if _, err := couponDao.ClaimWithDBLock(uid, batch.ID); err != nil {
			t.Fatalf("db 模式领券 user=%d: %v", uid, err)
		}
	}
	if _, err := couponDao.PersistClaim(3, batch.ID, batch.ValidDays); err != nil {
		t.Fatalf("Lua 路径落库最后一张: %v", err)
	}
	for uid := uint(4); uid <= 5; uid++ {
		if _, err := couponDao.PersistClaim(uid, batch.ID, batch.ValidDays); !errors.Is(err, cache.ErrCouponSoldOut) {
			t.Fatalf("超出总量的 Lua 落库应返回已抢光 user=%d, got %v", uid, err)
		}
	}
	if _, err := couponDao.ClaimWithDBLock(6, batch.ID); !errors.Is(err, cache.ErrCouponSoldOut) {
		t.Fatalf("超出总量的 db 模式领券应返回已抢光, got %v", err)
	}

	var issued int64
	if err := db.Model(&UserCoupon{}).Where("batch_id = ?", batch.ID).Count(&issued).Error; err != nil {
		t.Fatalf("count coupons: %v", err)
	}
	if issued != batch.Total {
		t.Fatalf("发放量 %d 应等于 total %d", issued, batch.Total)
	}
}

func openCouponTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:coupon-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	previous := dao.SetTestDB(db)
	t.Cleanup(func() {
		dao.SetTestDB(previous)
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&CouponBatch{}, &UserCoupon{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
