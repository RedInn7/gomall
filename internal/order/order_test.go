package order_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

func TestMerchantCannotShipAnotherMerchantsOrder(t *testing.T) {
	db := openOrderTestDB(t)
	seedOrderUser(t, db, 21, user.RoleMerchant)
	seedOrderUser(t, db, 22, user.RoleMerchant)
	seedOrderUser(t, db, 99, user.RoleAdmin)
	o := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderWaitShip, OrderNum: 5001})

	err := order.GetShippingSrv().ShipOrder(asUser(21), o.OrderNum, "SF1", "SF")
	if !errors.Is(err, order.ErrOrderForbidden) {
		t.Fatalf("merchant A shipped merchant B's order: err=%v", err)
	}
	assertOrderType(t, db, o.ID, consts.OrderWaitShip)

	if err := order.GetShippingSrv().ShipOrder(asUser(99), o.OrderNum, "SF1", "SF"); err != nil {
		t.Fatalf("admin ship: %v", err)
	}
	assertOrderType(t, db, o.ID, consts.OrderWaitReceive)
}

func openOrderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:order-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	previous := dao.SetTestDB(db)
	previousLogger := util.LogrusObj
	util.LogrusObj = logrus.New()
	t.Cleanup(func() {
		dao.SetTestDB(previous)
		util.LogrusObj = previousLogger
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&user.User{}, &order.Order{}, &clearing.PaymentClearing{}, &outbox.OutboxEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func asUser(id uint) context.Context {
	return ctl.NewContext(context.Background(), &ctl.UserInfo{Id: id})
}

func seedOrderUser(t *testing.T, db *gorm.DB, id uint, role string) {
	t.Helper()
	u := &user.User{UserName: fmt.Sprintf("order-u%d", id), Role: role}
	u.ID = id
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func seedOrder(t *testing.T, db *gorm.DB, o *order.Order) *order.Order {
	t.Helper()
	if o.ProductID == 0 {
		o.ProductID = 1
	}
	if o.AddressID == 0 {
		o.AddressID = 1
	}
	if o.Num == 0 {
		o.Num = 1
	}
	if o.Money == 0 {
		o.Money = 100
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return o
}

func assertOrderType(t *testing.T, db *gorm.DB, id uint, want uint) {
	t.Helper()
	var o order.Order
	if err := db.First(&o, id).Error; err != nil {
		t.Fatalf("load order: %v", err)
	}
	if o.Type != want {
		t.Fatalf("order %d type got %d want %d", id, o.Type, want)
	}
}
