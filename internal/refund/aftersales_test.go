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
	"github.com/RedInn7/gomall/internal/promo"
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

// 没有清算单的旧订单：卖家余额不足时退款卡住，卖家补足后补偿任务要能扫到并退完（并发跑也只退一次）；
// 上线前就已退款、refund_count=0 的历史单没有 credit 流水，不能被自动补钱。
func TestSettleCompensationRetriesNoClearingRefundAfterSellerFunded(t *testing.T) {
	db := openRefundTestDB(t)
	stubRestockRedis(t)
	seedRefundUser(t, db, 11, 0)
	seedRefundUser(t, db, 22, 100)
	seed := func(num uint64, refundCount int) *orderpkg.Order {
		o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300,
			Type: consts.OrderRefunded, RefundFromType: consts.OrderCompleted, RefundCount: refundCount, OrderNum: num}
		o.UpdatedAt = time.Now().Add(-time.Hour)
		if err := db.Create(o).Error; err != nil {
			t.Fatal(err)
		}
		return o
	}
	stuck := seed(9601, 1)
	seed(9602, 0) // 历史退款单

	if err := GetRefundSrv().SettleRefund(context.Background(), stuck.ID); !errors.Is(err, ErrSellerBalanceInsufficient) {
		t.Fatalf("want ErrSellerBalanceInsufficient, got %v", err)
	}
	GetRefundSrv().RunSettleCompensation() // 余额仍不足：补偿也退不了，不能扣成负数
	if got := refundUserBalance(t, db, 22); got != 100 {
		t.Fatalf("seller balance %d, want untouched 100", got)
	}

	db.Delete(&user.User{}, 22)
	seedRefundUser(t, db, 22, 500)
	runConcurrently(3, func(int) error { GetRefundSrv().RunSettleCompensation(); return nil })
	if got := refundUserBalance(t, db, 22); got != 200 {
		t.Fatalf("seller balance %d, want 200 (exactly one refund)", got)
	}
	if got := refundUserBalance(t, db, 11); got != 300 {
		t.Fatalf("buyer balance %d, want 300 (legacy refund_count=0 order must not be paid)", got)
	}
}

// 长期失败的单（卖家余额不足）排在前面超过一页，也不能把后面正常待补的退款饿死。
func TestSettleCompensationNotStarvedByPermanentFailures(t *testing.T) {
	db := openRefundTestDB(t)
	stubRestockRedis(t)
	seedRefundUser(t, db, 11, 0)
	seedRefundUser(t, db, 22, 1000)
	seedRefundUser(t, db, 23, 0) // 没钱的卖家
	seed := func(num uint64, boss uint) *orderpkg.Order {
		o := &orderpkg.Order{UserID: 11, BossID: boss, ProductID: 1, AddressID: 1, Num: 1, Money: 300,
			Type: consts.OrderRefunded, RefundFromType: consts.OrderCompleted, RefundCount: 1, OrderNum: num}
		o.UpdatedAt = time.Now().Add(-time.Hour)
		if err := db.Create(o).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&clearing.PaymentClearing{OrderID: o.ID, BuyerID: 11, SellerID: boss, Channel: clearing.ChannelWallet,
			GrossCents: 300, NetCents: 300, Currency: "CNY", Status: clearing.StatusSettled, ClearedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
		return o
	}
	for i := 0; i < compensationPageSize+5; i++ {
		seed(uint64(9700+i), 23)
	}
	good := seed(9900, 22)

	GetRefundSrv().RunSettleCompensation()
	var c clearing.PaymentClearing
	if err := db.Where("order_id = ?", good.ID).First(&c).Error; err != nil {
		t.Fatal(err)
	}
	if c.Status != clearing.StatusRefunded {
		t.Fatalf("order behind %d failing orders not compensated: clearing=%s", compensationPageSize+5, c.Status)
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

// promo.release 消费者进了 DLQ：补偿任务按台账补退满减预算，重复跑只退一次。
func TestSettleCompensationReleasesMissedPromoBudget(t *testing.T) {
	db := openRefundTestDB(t)
	stubRestockRedis(t)
	rule := &promo.PromoRule{Name: "满300减50", ThresholdCents: 300, DiscountCents: 50, ConsumedToday: 100,
		StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}
	if err := db.Create(rule).Error; err != nil {
		t.Fatal(err)
	}
	ago := time.Now().Add(-time.Hour)
	tooOld := time.Now().Add(-promoReleaseLookback - time.Hour)
	seed := func(num uint64, typ uint, updated time.Time) *orderpkg.Order {
		o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300, Type: typ, OrderNum: num,
			PromoRuleID: rule.ID, PromoDiscountCents: 50, FinalCents: 250}
		if err := db.Create(o).Error; err != nil {
			t.Fatal(err)
		}
		db.Model(o).UpdateColumn("updated_at", updated)
		return o
	}
	refunded := seed(9501, consts.OrderClosed, ago)
	cancelled := seed(9502, consts.OrderClosed, ago)
	db.Model(refunded).UpdateColumn("type", consts.OrderRefunded)
	seed(9503, consts.OrderClosed, tooOld) // 超出回看窗口，不补
	bulk := make([]uint, 0, compensationPageSize)
	for i := 0; i < compensationPageSize; i++ { // 凑满一页以上，一轮要全部补完
		bulk = append(bulk, seed(uint64(9600+i), consts.OrderClosed, ago).ID)
	}
	db.Model(rule).UpdateColumn("consumed_today", 100+50*compensationPageSize)

	runConcurrently(3, func(int) error { GetRefundSrv().RunSettleCompensation(); return nil })

	var releases []promo.PromoRelease
	db.Order("order_id").Find(&releases)
	if len(releases) != 2+len(bulk) {
		t.Fatalf("released %d orders, want %d", len(releases), 2+len(bulk))
	}
	releases = releases[:2]
	if releases[0].OrderID != refunded.ID || releases[0].Reason != "refund" ||
		releases[1].OrderID != cancelled.ID || releases[1].Reason != "cancel" {
		t.Fatalf("unexpected releases: %+v", releases)
	}
	var gotRule promo.PromoRule
	db.First(&gotRule, rule.ID)
	if gotRule.ConsumedToday != 0 {
		t.Fatalf("consumed_today=%d, want 0 (released exactly once per order)", gotRule.ConsumedToday)
	}
}

// 商家能看到自己待审批的退款和买家填的原因，看不到别家的。
func TestListRefundingScopedToMerchant(t *testing.T) {
	db := openRefundTestDB(t)
	seedRoleUser(t, db, 21, user.RoleMerchant)
	seedRoleUser(t, db, 22, user.RoleMerchant)
	mine := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300, Type: consts.OrderWaitShip, OrderNum: 9601}
	other := &orderpkg.Order{UserID: 11, BossID: 21, ProductID: 1, AddressID: 1, Num: 1, Money: 300, Type: consts.OrderWaitShip, OrderNum: 9602}
	for _, x := range []*orderpkg.Order{mine, other} {
		if err := db.Create(x).Error; err != nil {
			t.Fatal(err)
		}
		if err := GetRefundSrv().RequestRefund(ctxAs(11), x.OrderNum, "不想要了"); err != nil {
			t.Fatal(err)
		}
	}
	list, err := GetRefundSrv().ListRefunding(ctxAs(22), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != mine.ID || list[0].RefundReason != "不想要了" {
		t.Fatalf("unexpected list: %+v", list)
	}
}
