package groupbuy

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/refund"
	"gorm.io/gorm"
)

// accountNet 某系统账户 credit - debit 的净额（即账上还压着多少钱）。
func accountNet(t *testing.T, db *gorm.DB, accountCode string) int64 {
	t.Helper()
	var credit, debit int64
	db.Model(&money.AccountTransaction{}).Where("account_code=? AND direction=?", accountCode, money.DirectionCredit).
		Select("COALESCE(SUM(amount_cents),0)").Scan(&credit)
	db.Model(&money.AccountTransaction{}).Where("account_code=? AND direction=?", accountCode, money.DirectionDebit).
		Select("COALESCE(SUM(amount_cents),0)").Scan(&debit)
	return credit - debit
}

// 成团只把钱从拼团托管转进商户托管并建清算单，卖家此刻拿不到钱；
// 发货前退款走 merchant_escrow 原路退，订单完成后才由 clearing 结算给卖家。
func TestGroupSuccessClearsToMerchantEscrowNotSeller(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	seedGroupbuyUser(t, db, 2, 0) // 卖家
	seedGroupbuyUser(t, db, 3, 100000)
	p := seedGroupbuyProduct(t, db, 8000)

	ctx := context.Background()
	created, err := GetGroupbuySrv().CreateGroup(ctx, 1, p.ID, 2, 0, 0, 0, testPayKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := accountNet(t, db, money.AccountCodeGroupbuyEscrow); got != 8000 {
		t.Fatalf("开团后拼团托管应压 8000, got %d", got)
	}
	joined, err := GetGroupbuySrv().JoinGroup(ctx, 3, created.GroupID, 0, 0, testPayKey)
	if err != nil || !joined.IsSuccess {
		t.Fatalf("join: %+v %v", joined, err)
	}

	if got := groupbuyUserBalance(t, db, 2); got != 0 {
		t.Fatalf("成团时卖家不能直接入账, balance=%d", got)
	}
	if got := accountNet(t, db, money.AccountCodeGroupbuyEscrow); got != 0 {
		t.Fatalf("成团后拼团托管应清零, got %d", got)
	}
	if got := accountNet(t, db, money.AccountCodeMerchantEscrow); got != 16000 {
		t.Fatalf("成团后商户托管应压 16000, got %d", got)
	}
	var legacy int64
	db.Model(&money.AccountTransaction{}).Where("account_code=?", money.AccountCodeLegacySystem).Count(&legacy)
	if legacy != 0 {
		t.Fatalf("拼团不能再写 legacy_system, got %d rows", legacy)
	}

	// 团长订单发货前退款：从商户托管原路退回，卖家不受影响。
	var leaderOrder, memberOrder orderpkg.Order
	db.First(&leaderOrder, created.OrderID)
	db.First(&memberOrder, joined.OrderID)
	var rec clearing.PaymentClearing
	if err := db.Where("order_id=?", leaderOrder.ID).First(&rec).Error; err != nil || rec.Status != clearing.StatusCleared || rec.GrossCents != 8000 {
		t.Fatalf("成团应建清算单: %+v err=%v", rec, err)
	}
	db.Model(&orderpkg.Order{}).Where("id=?", leaderOrder.ID).Update("type", consts.OrderRefunded)
	if err := refund.GetRefundSrv().SettleRefund(ctx, leaderOrder.ID); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if got := groupbuyUserBalance(t, db, 1); got != 100000 {
		t.Fatalf("团长应全额退回, balance=%d", got)
	}
	if got := groupbuyUserBalance(t, db, 2); got != 0 {
		t.Fatalf("发货前退款不能扣卖家, balance=%d", got)
	}

	// 成员订单履约完成：清算结算给卖家。
	db.Model(&orderpkg.Order{}).Where("id=?", memberOrder.ID).Update("type", consts.OrderCompleted)
	if err := clearing.SettleCompletedOrder(ctx, memberOrder.ID); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if got := groupbuyUserBalance(t, db, 2); got != 8000 {
		t.Fatalf("订单完成后卖家应入账 8000, got %d", got)
	}
	if got := accountNet(t, db, money.AccountCodeMerchantEscrow); got != 0 {
		t.Fatalf("商户托管应清零, got %d", got)
	}
}
