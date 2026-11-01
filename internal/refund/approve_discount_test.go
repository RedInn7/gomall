package refund

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/coupon"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/service/events"
)

// 满减 + 券全额抵扣到 0 的订单：order.refunded 的 amount 与实际打款同口径为 0（原来会带出折前原价），
// 核销的券在批准退款的同一事务里退回，事件带上扣预算的那一天。
func TestApproveRefundUsesPayableAmountAndReleasesCoupon(t *testing.T) {
	db := openRefundTestDB(t)
	if err := db.AutoMigrate(&coupon.CouponBatch{}, &coupon.UserCoupon{}); err != nil {
		t.Fatal(err)
	}
	seedRoleUser(t, db, 99, user.RoleAdmin)
	batch := &coupon.CouponBatch{Name: "c", Type: coupon.CouponTypeAmount, Amount: 100, Total: 5, PerUser: 1, ValidDays: 7,
		StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}
	if err := db.Create(batch).Error; err != nil {
		t.Fatal(err)
	}
	cdao := coupon.NewCouponDao(context.Background())
	uc, err := cdao.PersistClaim(11, batch.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	o := &orderpkg.Order{
		UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300,
		Type: consts.OrderRefunding, RefundFromType: consts.OrderWaitShip, OrderNum: 8801,
		PromoRuleID: 5, PromoDiscountCents: 200, UserCouponID: uc.ID, CouponDiscountCents: 100, FinalCents: 0,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatal(err)
	}
	if err := cdao.UseInTx(uc.ID, 11, o.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	admin := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 99})
	if err := GetRefundSrv().ApproveRefund(admin, o.OrderNum); err != nil {
		t.Fatal(err)
	}

	var row outbox.OutboxEvent
	if err := db.Where("routing_key = ? AND aggregate_id = ?", "order.refunded", o.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var evt events.OrderRefundedEvent
	if err := json.Unmarshal([]byte(row.Payload), &evt); err != nil {
		t.Fatal(err)
	}
	if evt.Amount != 0 || evt.Amount != refundAmount(o) {
		t.Fatalf("event amount = %d, want 0 (same as refundAmount)", evt.Amount)
	}
	if evt.PromoBudgetDate != promo.BudgetDay(o.CreatedAt) {
		t.Fatalf("event budget day = %q", evt.PromoBudgetDate)
	}
	var c coupon.UserCoupon
	db.First(&c, uc.ID)
	if c.Status != coupon.UserCouponStatusUnused || c.UsedOrderId != 0 {
		t.Fatalf("coupon not released on refund: %+v", c)
	}
}
