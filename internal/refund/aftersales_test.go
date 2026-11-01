package refund

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/service/events"
)

func ctxAs(id uint) context.Context {
	return ctl.NewContext(context.Background(), &ctl.UserInfo{Id: id})
}

// runConcurrently 同时放出 n 个 goroutine 执行 fn，返回各自的错误。
func runConcurrently(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

// 已完成订单超过售后期不能再退；同一订单最多申请 3 次；并发申请只受理一次；原因落库。
func TestRequestRefundEnforcesAfterSalesWindowAndLimit(t *testing.T) {
	db := openRefundTestDB(t)
	seedRoleUser(t, db, 22, user.RoleMerchant)
	expired := time.Now().Add(-afterSalesWindow - time.Hour)
	inWindow := time.Now().Add(-24 * time.Hour)
	old := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300, Type: consts.OrderCompleted, OrderNum: 9001, CompletedAt: &expired}
	o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300, Type: consts.OrderCompleted, OrderNum: 9002, CompletedAt: &inWindow}
	for _, x := range []*orderpkg.Order{old, o} {
		if err := db.Create(x).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := GetRefundSrv().RequestRefund(ctxAs(11), old.OrderNum, "坏了"); !errors.Is(err, ErrAfterSalesExpired) {
		t.Fatalf("expired order refund: %v", err)
	}
	assertRefundOrderType(t, db, old.ID, consts.OrderCompleted)

	for round := 1; round <= maxRefundRequests; round++ {
		errs := runConcurrently(6, func(int) error { return GetRefundSrv().RequestRefund(ctxAs(11), o.OrderNum, "尺码不对") })
		okCount := 0
		for _, err := range errs {
			if err == nil {
				okCount++
			} else if !errors.Is(err, orderpkg.ErrInvalidOrderStateTransition) {
				t.Fatalf("round %d: unexpected err %v", round, err)
			}
		}
		if okCount != 1 {
			t.Fatalf("round %d: concurrent requests accepted %d times, want 1", round, okCount)
		}
		var got orderpkg.Order
		db.First(&got, o.ID)
		if got.RefundCount != round || got.RefundReason != "尺码不对" {
			t.Fatalf("round %d: count=%d reason=%q", round, got.RefundCount, got.RefundReason)
		}
		if err := GetRefundSrv().RejectRefund(ctxAs(22), o.OrderNum, "no"); err != nil {
			t.Fatal(err)
		}
	}
	if err := GetRefundSrv().RequestRefund(ctxAs(11), o.OrderNum, "再试一次"); !errors.Is(err, ErrTooManyRefundRequests) {
		t.Fatalf("4th request must be refused, got %v", err)
	}
	assertRefundOrderType(t, db, o.ID, consts.OrderCompleted)
}

// 买家申请退款再被驳回，不能把自动确认收货的 7 天重新计时。
func TestRejectedRefundDoesNotResetAutoConfirmClock(t *testing.T) {
	db := openRefundTestDB(t)
	seedRoleUser(t, db, 22, user.RoleMerchant)
	shipped := time.Now().Add(-8 * 24 * time.Hour)
	o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300, Type: consts.OrderWaitReceive, OrderNum: 9101, ShippedAt: &shipped}
	if err := db.Create(o).Error; err != nil {
		t.Fatal(err)
	}
	if err := GetRefundSrv().RequestRefund(ctxAs(11), o.OrderNum, "没收到"); err != nil {
		t.Fatal(err)
	}
	if err := GetRefundSrv().RejectRefund(ctxAs(22), o.OrderNum, "物流显示已签收"); err != nil {
		t.Fatal(err)
	}
	(&orderpkg.OrderTaskService{}).RunAutoConfirmReceive()
	assertRefundOrderType(t, db, o.ID, consts.OrderCompleted)
}

// 满减抵扣到 0 的订单，order.refunded 事件金额要和实际退款一致（0），不能是原价。
func TestApproveRefundEventAmountMatchesSettlement(t *testing.T) {
	db := openRefundTestDB(t)
	seedRoleUser(t, db, 22, user.RoleMerchant)
	o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300,
		PromoRuleID: 1, PromoDiscountCents: 300, FinalCents: 0,
		Type: consts.OrderRefunding, RefundFromType: consts.OrderWaitShip, OrderNum: 9201}
	if err := db.Create(o).Error; err != nil {
		t.Fatal(err)
	}
	if err := GetRefundSrv().ApproveRefund(ctxAs(22), o.OrderNum); err != nil {
		t.Fatal(err)
	}
	var row outbox.OutboxEvent
	if err := db.Where("routing_key=? AND aggregate_id=?", "order.refunded", o.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var evt events.OrderRefundedEvent
	if err := json.Unmarshal([]byte(row.Payload), &evt); err != nil {
		t.Fatal(err)
	}
	if evt.Amount != refundAmount(o) || evt.Amount != 0 {
		t.Fatalf("event amount %d, settlement amount %d", evt.Amount, refundAmount(o))
	}
}

// 已结算给卖家的订单退款时卖家余额不够：不扣成负数、买家也先不入账；卖家补足后补偿任务自动退完。
func TestSettleRefundNeverDrivesSellerNegative(t *testing.T) {
	db := openRefundTestDB(t)
	stubRestockRedis(t)
	seedRefundUser(t, db, 11, 0)
	seedRefundUser(t, db, 22, 100)
	o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300,
		Type: consts.OrderRefunded, RefundFromType: consts.OrderCompleted, OrderNum: 9301}
	o.UpdatedAt = time.Now().Add(-time.Hour)
	if err := db.Create(o).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&clearing.PaymentClearing{OrderID: o.ID, BuyerID: 11, SellerID: 22, Channel: clearing.ChannelWallet,
		GrossCents: 300, NetCents: 300, Currency: "CNY", Status: clearing.StatusSettled, ClearedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	errs := runConcurrently(4, func(int) error { return GetRefundSrv().SettleRefund(context.Background(), o.ID) })
	for _, err := range errs {
		if !errors.Is(err, ErrSellerBalanceInsufficient) {
			t.Fatalf("want ErrSellerBalanceInsufficient, got %v", err)
		}
	}
	if got := refundUserBalance(t, db, 22); got != 100 {
		t.Fatalf("seller balance %d, want untouched 100", got)
	}
	if got := refundUserBalance(t, db, 11); got != 0 {
		t.Fatalf("buyer credited %d before seller funded", got)
	}

	// 卖家补足余额后，补偿任务把这笔退完
	db.Delete(&user.User{}, 22)
	seedRefundUser(t, db, 22, 500)
	GetRefundSrv().RunSettleCompensation()
	if got := refundUserBalance(t, db, 22); got != 200 {
		t.Fatalf("seller balance %d, want 200", got)
	}
	if got := refundUserBalance(t, db, 11); got != 300 {
		t.Fatalf("buyer balance %d, want 300", got)
	}
}

// 发货后才退的订单：结算不回补库存；商家确认退货入库才补，并发点多次只补一次；未发货的退款不能再走这条路。
func TestReturnReceivedRestocksShippedRefundOnce(t *testing.T) {
	db := openRefundTestDB(t)
	restocks := stubRestockRedis(t)
	seedRoleUser(t, db, 22, user.RoleMerchant)
	seedRoleUser(t, db, 21, user.RoleMerchant)
	p := &product.Product{Name: "book", CategoryID: 1, Num: 0, BossID: 22}
	if err := db.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	shipped := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: p.ID, AddressID: 1, Num: 2, Money: 300,
		Type: consts.OrderRefunded, RefundFromType: consts.OrderWaitReceive, OrderNum: 9401}
	unshipped := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: p.ID, AddressID: 1, Num: 1, Money: 300,
		Type: consts.OrderRefunded, RefundFromType: consts.OrderWaitShip, OrderNum: 9402}
	for _, x := range []*orderpkg.Order{shipped, unshipped} {
		if err := db.Create(x).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := GetRefundSrv().ConfirmReturnReceived(ctxAs(21), shipped.OrderNum); !errors.Is(err, orderpkg.ErrOrderForbidden) {
		t.Fatalf("other merchant confirmed return: %v", err)
	}
	if err := GetRefundSrv().ConfirmReturnReceived(ctxAs(22), unshipped.OrderNum); !errors.Is(err, ErrNotReturnable) {
		t.Fatalf("unshipped refund must not be restocked via return: %v", err)
	}

	errs := runConcurrently(8, func(int) error { return GetRefundSrv().ConfirmReturnReceived(ctxAs(22), shipped.OrderNum) })
	okCount := 0
	for _, err := range errs {
		if err == nil {
			okCount++
		} else if !errors.Is(err, ErrNotReturnable) {
			t.Fatalf("unexpected err %v", err)
		}
	}
	if okCount != 1 {
		t.Fatalf("return confirmed %d times, want 1", okCount)
	}
	var got product.Product
	db.First(&got, p.ID)
	if got.Num != 2 || len(*restocks) != 1 {
		t.Fatalf("stock=%d redis=%v, want restocked once by 2", got.Num, *restocks)
	}
}
