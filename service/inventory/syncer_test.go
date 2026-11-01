package inventory

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// available 漂移按 Num + 待核销 - reserved 纠正；欠账（负 available）不被夹到 0；待核销的已付单不被当成多出来的库存。
func TestReconcileAvailable(t *testing.T) {
	util.LogrusObj = logrus.New()
	util.LogrusObj.Out = io.Discard
	availableReconcileGrace = 0
	mr := miniredis.RunT(t)
	prevRedis := cache.RedisClient
	cache.RedisClient = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:inv-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	prevDB := dao.SetTestDB(db)
	t.Cleanup(func() { dao.SetTestDB(prevDB); cache.RedisClient = prevRedis })
	if err := db.AutoMigrate(&product.Product{}, &order.Order{}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		num, available, reserved, pending, paying, want int64
	}{
		{num: 10, available: 3, reserved: 2, want: 8},            // 退款回补写 Redis 失败：补回
		{num: 1, available: 0, reserved: 3, want: -2},            // 欠账被错误清零：恢复成负数
		{num: 5, available: 5, reserved: 1, pending: 1, want: 5}, // 已付待核销：不动
		{num: 4, available: 9, reserved: 0, want: 4},             // Redis 多了：扣回
		{num: 4, available: 4, reserved: 1, paying: 1, want: 4},  // 刚付款、reserved 还没核销：旧算法会扣成 3
		{num: 4, available: 9, reserved: 1, paying: 1, want: 4},  // 刚付款之外还多了：只扣确定多的部分
	}
	for i, c := range cases {
		p := &product.Product{Name: fmt.Sprint(i), Num: int(c.num)}
		if err := db.Create(p).Error; err != nil {
			t.Fatal(err)
		}
		mr.Set(cache.StockAvailableKey(p.ID), fmt.Sprint(c.available))
		mr.Set(cache.StockReservedKey(p.ID), fmt.Sprint(c.reserved))
		if c.pending > 0 {
			o := &order.Order{ProductID: p.ID, UserID: 1, BossID: 1, Num: int(c.pending), Type: consts.OrderWaitShip, StockCommitPending: true}
			if err := db.Create(o).Error; err != nil {
				t.Fatal(err)
			}
		}
		if c.paying > 0 { // 支付刚推进到 WaitShip（DB 已扣），还没核销 reserved
			o := &order.Order{ProductID: p.ID, UserID: 1, BossID: 1, Num: int(c.paying), Type: consts.OrderWaitShip}
			if err := db.Create(o).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := ReconcileAvailable(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		got, _ := cache.RedisClient.Get(context.Background(), cache.StockAvailableKey(uint(i+1))).Int64()
		if got != c.want {
			t.Errorf("case %d available=%d want %d", i, got, c.want)
		}
	}
}
