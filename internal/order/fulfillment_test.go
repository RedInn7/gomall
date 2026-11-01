package order_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/types"
)

// 下单后改地址、删地址都不能改写订单上的收货地址：同步下单直接拿快照，异步下单快照随任务带到消费侧。
func TestOrderAddressSnapshotSurvivesAddressEditAndDelete(t *testing.T) {
	db := openOrderTestDB(t)
	stubStockRedis(t, map[string]int64{})
	snowflake.InitSnowflake(1)
	if err := db.AutoMigrate(&address.Address{}, &product.Product{}); err != nil {
		t.Fatal(err)
	}
	addr := &address.Address{UserID: 11, Name: "张三", Phone: "13800000000", Address: "上海市徐汇区1号"}
	if err := db.Create(addr).Error; err != nil {
		t.Fatal(err)
	}
	addrDao := address.NewAddressDaoByDB(db)
	if _, err := addrDao.OwnedSnapshot(addr.ID, 12); !errors.Is(err, address.ErrAddressNotOwned) {
		t.Fatalf("other user's address must be rejected, got %v", err)
	}
	snap, err := addrDao.OwnedSnapshot(addr.ID, 11)
	if err != nil {
		t.Fatal(err)
	}
	seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderWaitShip, OrderNum: 8001, AddressID: addr.ID, ShipTo: snap})

	// 异步下单：任务里带的快照落进订单
	p := &product.Product{Name: "mug", CategoryID: 1, Price: "10.00", Num: 5, OnSale: true, BossID: 22}
	if err := db.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	prev := order.AsyncOrderTicketStoreInstance()
	order.SetAsyncOrderTicketStore(memTicketStore{})
	t.Cleanup(func() { order.SetAsyncOrderTicketStore(prev) })
	body, _ := json.Marshal(order.AsyncOrderTask{Ticket: "77", UserID: 11, ProductID: p.ID, Num: 1, AddressID: addr.ID, ShipTo: snap})
	if err := order.HandleAsyncOrderTask(context.Background(), body); err != nil {
		t.Fatal(err)
	}

	if err := addrDao.UpdateAddressById(addr.ID, 11, &address.Address{Name: "李四", Phone: "13900000000", Address: "北京市朝阳区2号"}); err != nil {
		t.Fatal(err)
	}
	if err := addrDao.DeleteAddressById(addr.ID, 11); err != nil {
		t.Fatal(err)
	}

	for _, typ := range []int{int(consts.OrderWaitShip), int(consts.OrderWaitPay)} {
		resp, err := order.NewOrderDaoByDB(db).ListOrderByCondition(11, &order.OrderListReq{Type: typ, BasePage: types.BasePage{PageSize: 10}})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.List) != 1 {
			t.Fatalf("type %d: want 1 order, got %d", typ, len(resp.List))
		}
		got := resp.List[0]
		if got.AddressName != "张三" || got.AddressPhone != "13800000000" || got.Address != "上海市徐汇区1号" {
			t.Fatalf("type %d: order address rewritten by address edit/delete: %+v", typ, got)
		}
	}
}

func TestAddressUpdateAndDeleteRejectForeignOrMissingID(t *testing.T) {
	db := openOrderTestDB(t)
	if err := db.AutoMigrate(&address.Address{}); err != nil {
		t.Fatal(err)
	}
	addr := &address.Address{UserID: 11, Name: "张三", Phone: "13800000000", Address: "a"}
	if err := db.Create(addr).Error; err != nil {
		t.Fatal(err)
	}
	d := address.NewAddressDaoByDB(db)
	if err := d.UpdateAddressById(addr.ID, 12, &address.Address{Name: "x", Phone: "13900000000", Address: "b"}); !errors.Is(err, address.ErrAddressNotOwned) {
		t.Fatalf("update foreign address: %v", err)
	}
	if err := d.DeleteAddressById(addr.ID, 12); !errors.Is(err, address.ErrAddressNotOwned) {
		t.Fatalf("delete foreign address: %v", err)
	}
	if err := d.DeleteAddressById(999, 11); !errors.Is(err, address.ErrAddressNotOwned) {
		t.Fatalf("delete missing address: %v", err)
	}
	// 同内容再提交一次不能被当成"不属于你"
	same := &address.Address{Name: addr.Name, Phone: addr.Phone, Address: addr.Address}
	if err := d.UpdateAddressById(addr.ID, 11, same); err != nil {
		t.Fatalf("idempotent update: %v", err)
	}
}

// 空单号不能发货；多个人同时点发货只有一个成功，单号、承运商、发货时间都落库，只发一条 order.shipped。
func TestShipOrderRequiresTrackingAndPersistsItUnderConcurrency(t *testing.T) {
	db := openOrderTestDB(t)
	seedOrderUser(t, db, 22, user.RoleMerchant)
	seedOrderUser(t, db, 99, user.RoleAdmin)
	o := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderWaitShip, OrderNum: 5101})

	if err := order.GetShippingSrv().ShipOrder(asUser(22), o.OrderNum, "  ", "SF"); !errors.Is(err, order.ErrTrackingRequired) {
		t.Fatalf("blank tracking must be rejected, got %v", err)
	}
	assertOrderType(t, db, o.ID, consts.OrderWaitShip)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		operator := uint(22)
		if i%2 == 1 {
			operator = 99
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- order.GetShippingSrv().ShipOrder(asUser(operator), o.OrderNum, "SF123", "SF")
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	okCount := 0
	for err := range errs {
		if err == nil {
			okCount++
		} else if !errors.Is(err, order.ErrInvalidOrderStateTransition) {
			t.Fatalf("unexpected ship error: %v", err)
		}
	}
	if okCount != 1 {
		t.Fatalf("concurrent ship succeeded %d times, want 1", okCount)
	}
	var got order.Order
	if err := db.First(&got, o.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Type != consts.OrderWaitReceive || got.TrackingNo != "SF123" || got.Carrier != "SF" || got.ShippedAt == nil {
		t.Fatalf("shipping info not persisted: %+v", got)
	}
	var events int64
	db.Model(&outbox.OutboxEvent{}).Where("routing_key=? AND aggregate_id=?", "order.shipped", o.ID).Count(&events)
	if events != 1 {
		t.Fatalf("order.shipped written %d times, want 1", events)
	}
}

// 自动确认收货按发货时间计时：退款申请 / 驳回刷新 updated_at 不能把 7 天重新算；
// 积压超过一页的订单一轮跑完；和买家手动确认并发时每单只完成一次。
func TestAutoConfirmReceiveUsesShippedAtAndDrainsBacklog(t *testing.T) {
	db := openOrderTestDB(t)
	old := time.Now().Add(-8 * 24 * time.Hour)
	recent := time.Now().Add(-24 * time.Hour)

	// 发货 8 天但 updated_at 刚被刷新（驳回退款）——应当确认
	touched := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderWaitReceive, OrderNum: 1, ShippedAt: &old})
	db.Model(&order.Order{}).Where("id=?", touched.ID).UpdateColumn("updated_at", time.Now())
	// 发货 1 天但 updated_at 很旧——不应确认
	fresh := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderWaitReceive, OrderNum: 2, ShippedAt: &recent,
		Model: dbmodel.Model{UpdatedAt: old}})
	db.Model(&order.Order{}).Where("id=?", fresh.ID).UpdateColumn("updated_at", old)
	// 超过一页的积压
	const backlog = 230
	for i := 0; i < backlog; i++ {
		seedOrder(t, db, &order.Order{UserID: 12, BossID: 22, Type: consts.OrderWaitReceive, OrderNum: uint64(100 + i), ShippedAt: &old})
	}

	// 买家同时手动确认其中一部分
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		(&order.OrderTaskService{}).RunAutoConfirmReceive()
	}()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(num uint64) {
			defer wg.Done()
			_ = order.GetShippingSrv().ConfirmReceive(asUser(12), num)
		}(uint64(100 + i))
	}
	wg.Wait()

	assertOrderType(t, db, touched.ID, consts.OrderCompleted)
	assertOrderType(t, db, fresh.ID, consts.OrderWaitReceive)
	var left int64
	db.Model(&order.Order{}).Where("user_id=? AND type<>?", 12, consts.OrderCompleted).Count(&left)
	if left != 0 {
		t.Fatalf("%d backlog orders left unconfirmed after one run", left)
	}
	var noCompletedAt int64
	db.Model(&order.Order{}).Where("type=? AND completed_at IS NULL", consts.OrderCompleted).Count(&noCompletedAt)
	if noCompletedAt != 0 {
		t.Fatalf("%d completed orders without completed_at", noCompletedAt)
	}
	var completedEvents int64
	db.Model(&outbox.OutboxEvent{}).Where("routing_key=?", "order.completed").Count(&completedEvents)
	if completedEvents != backlog+1 {
		t.Fatalf("order.completed written %d times, want %d (one per order)", completedEvents, backlog+1)
	}
}

func TestBackfillFulfillmentColumns(t *testing.T) {
	db := openOrderTestDB(t)
	if err := db.AutoMigrate(&address.Address{}); err != nil {
		t.Fatal(err)
	}
	addr := &address.Address{UserID: 11, Name: "张三", Phone: "13800000000", Address: "上海"}
	if err := db.Create(addr).Error; err != nil {
		t.Fatal(err)
	}
	waitReceive := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderWaitReceive, OrderNum: 1, AddressID: addr.ID})
	completed := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderCompleted, OrderNum: 2, AddressID: 404})
	refundingFromShip := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderRefunding, RefundFromType: consts.OrderWaitShip, OrderNum: 3})
	settledRefund := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderRefunded, OrderNum: 4})
	pendingRefund := seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, Type: consts.OrderRefunded, OrderNum: 5})
	if err := money.NewLedgerDaoByDB(db).AppendTransaction(11, settledRefund.ID, money.DirectionCredit, 100, 100, money.BizTypeRefund); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // 可重复执行
		if err := order.BackfillFulfillmentColumns(db); err != nil {
			t.Fatalf("backfill run %d: %v", i+1, err)
		}
	}
	load := func(id uint) order.Order {
		var o order.Order
		if err := db.First(&o, id).Error; err != nil {
			t.Fatal(err)
		}
		return o
	}
	if o := load(waitReceive.ID); o.ShippedAt == nil || o.ShipTo.Name != "张三" || o.ShipTo.Detail != "上海" {
		t.Fatalf("wait-receive not backfilled: %+v", o)
	}
	if o := load(completed.ID); o.ShippedAt == nil || o.CompletedAt == nil || o.ShipTo.Name != "" {
		t.Fatalf("completed not backfilled correctly: %+v", o)
	}
	if o := load(refundingFromShip.ID); o.ShippedAt != nil {
		t.Fatalf("never-shipped order got shipped_at: %+v", o)
	}
	if o := load(settledRefund.ID); o.RestockedAt == nil {
		t.Fatalf("settled legacy refund must be marked restocked")
	}
	if o := load(pendingRefund.ID); o.RestockedAt != nil {
		t.Fatalf("unsettled refund must not be marked restocked")
	}
}
