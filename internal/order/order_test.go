package order_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
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

func TestDepositPaidPreorderIsNotTreatedAsUnpaid(t *testing.T) {
	db := openOrderTestDB(t)
	longAgo := time.Now().Add(-time.Hour)
	preorder := seedOrder(t, db, &order.Order{
		UserID: 11, BossID: 22, Type: consts.OrderWaitPay, OrderNum: 6001, PreorderStage: 1,
		Model: dbmodel.Model{CreatedAt: longAgo},
	})
	normal := seedOrder(t, db, &order.Order{
		UserID: 11, BossID: 22, Type: consts.OrderWaitPay, OrderNum: 6002,
		Model: dbmodel.Model{CreatedAt: longAgo},
	})
	orderDao := order.NewOrderDaoByDB(db)

	timedOut, err := orderDao.GetTimeoutOrders(15, 100)
	if err != nil {
		t.Fatalf("scan timeout orders: %v", err)
	}
	if len(timedOut) != 1 || timedOut[0].ID != normal.ID {
		t.Fatalf("timeout scan must only return the normal order, got %+v", timedOut)
	}

	// MQ 延迟关单与 cron 都走 CancelUnpaidOrder -> CloseOrderWithCheck。
	if err := order.CancelUnpaidOrder(preorder.OrderNum); err != nil {
		t.Fatalf("cancel preorder: %v", err)
	}
	assertOrderType(t, db, preorder.ID, consts.OrderWaitPay)
	if closed, err := orderDao.CloseOrderWithCheck(normal.OrderNum); err != nil || !closed {
		t.Fatalf("normal unpaid order should close: closed=%v err=%v", closed, err)
	}

	// 普通支付确认的状态守卫同样不认预售单。
	if paid, err := orderDao.MarkOrderPaidWithCheck(preorder.ID, preorder.UserID); err != nil || paid {
		t.Fatalf("preorder must not be marked paid by normal payment: paid=%v err=%v", paid, err)
	}
	assertOrderType(t, db, preorder.ID, consts.OrderWaitPay)
}

func TestOrderDeleteOnlyRemovesOrdersWithSettledFunds(t *testing.T) {
	db := openOrderTestDB(t)
	tests := []struct {
		name          string
		orderType     uint
		clearing      string // 空串表示没有清算单
		refundCredit  string // 买家退款入账流水的 biz_type，空串表示退款还没落账
		owner         uint
		wantDeletable bool
	}{
		{name: "wait ship", orderType: consts.OrderWaitShip, clearing: clearing.StatusCleared, owner: 11},
		{name: "wait receive", orderType: consts.OrderWaitReceive, clearing: clearing.StatusCleared, owner: 11},
		{name: "refunding", orderType: consts.OrderRefunding, clearing: clearing.StatusCleared, owner: 11},
		{name: "completed before settlement", orderType: consts.OrderCompleted, clearing: clearing.StatusCleared, owner: 11},
		{name: "other buyer's closed order", orderType: consts.OrderClosed, owner: 12},
		// 已批准退款、但 order.refunded 消费者还没把钱退到账：删了单退款就永远到不了。
		{name: "refunded before credit with settled clearing", orderType: consts.OrderRefunded, clearing: clearing.StatusSettled, owner: 11},
		{name: "refunded legacy order before credit", orderType: consts.OrderRefunded, owner: 11},
		{name: "closed", orderType: consts.OrderClosed, owner: 11, wantDeletable: true},
		{name: "completed and settled", orderType: consts.OrderCompleted, clearing: clearing.StatusSettled, owner: 11, wantDeletable: true},
		{name: "refund credited from escrow", orderType: consts.OrderRefunded, clearing: clearing.StatusRefunded, refundCredit: money.BizTypeEscrowRefund, owner: 11, wantDeletable: true},
		{name: "refund credited for legacy order", orderType: consts.OrderRefunded, refundCredit: money.BizTypeRefund, owner: 11, wantDeletable: true},
		{name: "completed legacy order without clearing", orderType: consts.OrderCompleted, owner: 11, wantDeletable: true},
	}
	srv := order.NewOrderSrv(nil)
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 显式 id：sqlite 会复用刚删掉的最大 id，导致残留清算单撞上新订单。
			o := seedOrder(t, db, &order.Order{
				Model:  dbmodel.Model{ID: uint(100 + i)},
				UserID: tt.owner, BossID: 22, Type: tt.orderType, OrderNum: uint64(8000 + i),
			})
			if tt.clearing != "" {
				record := &clearing.PaymentClearing{
					OrderID: o.ID, BuyerID: o.UserID, SellerID: o.BossID, Channel: clearing.ChannelWallet,
					GrossCents: 100, NetCents: 100, Currency: "CNY", Status: tt.clearing, ClearedAt: time.Now(),
				}
				if err := db.Create(record).Error; err != nil {
					t.Fatalf("seed clearing: %v", err)
				}
			}
			if tt.refundCredit != "" {
				credit := &money.AccountTransaction{
					UserID: o.UserID, Direction: money.DirectionCredit, AmountCents: 100,
					RefOrderID: o.ID, BalanceAfterCents: 100, BizType: tt.refundCredit,
				}
				if err := db.Create(credit).Error; err != nil {
					t.Fatalf("seed refund credit: %v", err)
				}
			}

			_, err := srv.OrderDelete(asUser(11), &order.OrderDeleteReq{OrderId: o.ID})
			var left int64
			if cerr := db.Model(&order.Order{}).Where("id = ?", o.ID).Count(&left).Error; cerr != nil {
				t.Fatalf("count order: %v", cerr)
			}
			if tt.wantDeletable && (err != nil || left != 0) {
				t.Fatalf("order should be deleted: err=%v left=%d", err, left)
			}
			if !tt.wantDeletable && (err == nil || left != 1) {
				t.Fatalf("order must survive delete: err=%v left=%d", err, left)
			}
		})
	}
}

func openOrderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 与生产同一命名策略（单数表名），DAO 里的原生 SQL 才能对上表名。
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:order-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
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
	if err := db.AutoMigrate(&user.User{}, &order.Order{}, &clearing.PaymentClearing{}, &money.AccountTransaction{}, &outbox.OutboxEvent{}); err != nil {
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
