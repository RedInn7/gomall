package clearing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/user"
)

func TestRecordClearedTxRejectsChannelSourceMismatch(t *testing.T) {
	db := openClearingTestDB(t)
	o := &order.Order{UserID: 1, BossID: 2, Num: 1, Money: 100}
	o.ID = 9
	after := int64(0)

	tests := []struct {
		name    string
		channel string
		balance *int64
	}{
		{name: "wallet without wallet balance", channel: ChannelWallet, balance: nil},
		{name: "external channel with wallet balance", channel: ChannelStripe, balance: &after},
		{name: "unknown channel", channel: "cash", balance: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RecordClearedTx(db, o, tt.channel, "", "USD", tt.balance)
			if !errors.Is(err, ErrInvalidClearingInput) {
				t.Fatalf("want ErrInvalidClearingInput, got %v", err)
			}
		})
	}
}

func TestClearingDefersSellerCreditAndSettlementIsIdempotent(t *testing.T) {
	db := openClearingTestDB(t)
	seedUser(t, db, 11, 700)
	seedUser(t, db, 22, 500)
	o := seedOrder(t, db, 101, 11, 22, consts.OrderWaitShip, 300)

	buyerAfter := int64(700)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return RecordClearedTx(tx, o, ChannelWallet, "", "usd", &buyerAfter)
	}); err != nil {
		t.Fatalf("record clearing: %v", err)
	}

	if got := userBalance(t, db, 22); got != 500 {
		t.Fatalf("seller must not receive funds at clearing time: got %d want 500", got)
	}
	assertLedgerEntry(t, db, o.ID, money.BizTypeOrderClear, money.DirectionCredit, money.AccountCodeMerchantEscrow, 300)
	assertLedgerEntry(t, db, o.ID, money.BizTypeOrderClear, money.DirectionDebit, money.AccountCodeUserWallet, 300)

	if err := settleCompletedOrder(db, o.ID); !errors.Is(err, ErrOrderNotCompleted) {
		t.Fatalf("settlement before completion should fail, got %v", err)
	}
	if got := userBalance(t, db, 22); got != 500 {
		t.Fatalf("failed settlement changed seller balance: got %d", got)
	}

	if err := db.Model(&order.Order{}).Where("id = ?", o.ID).Update("type", consts.OrderCompleted).Error; err != nil {
		t.Fatalf("complete order: %v", err)
	}
	if err := settleCompletedOrder(db, o.ID); err != nil {
		t.Fatalf("first settlement: %v", err)
	}
	if err := settleCompletedOrder(db, o.ID); err != nil {
		t.Fatalf("duplicate settlement: %v", err)
	}

	if got := userBalance(t, db, 22); got != 800 {
		t.Fatalf("seller should be credited exactly once: got %d want 800", got)
	}
	assertLedgerEntry(t, db, o.ID, money.BizTypeOrderSettle, money.DirectionDebit, money.AccountCodeMerchantEscrow, 300)
	assertLedgerEntry(t, db, o.ID, money.BizTypeOrderSettle, money.DirectionCredit, money.AccountCodeUserWallet, 300)

	var record PaymentClearing
	if err := db.Where("order_id = ?", o.ID).First(&record).Error; err != nil {
		t.Fatalf("load clearing: %v", err)
	}
	if record.Status != StatusSettled || record.SettledAt == nil {
		t.Fatalf("unexpected clearing state: status=%s settled_at=%v", record.Status, record.SettledAt)
	}
	var settledEntries int64
	if err := db.Model(&money.AccountTransaction{}).
		Where("ref_order_id = ? AND biz_type = ?", o.ID, money.BizTypeOrderSettle).
		Count(&settledEntries).Error; err != nil {
		t.Fatalf("count settlement entries: %v", err)
	}
	if settledEntries != 2 {
		t.Fatalf("duplicate event wrote duplicate ledger entries: got %d want 2", settledEntries)
	}
}

func TestSettlementRollsBackSellerBalanceWhenLedgerWriteFails(t *testing.T) {
	db := openClearingTestDB(t)
	seedUser(t, db, 61, 0)
	seedUser(t, db, 62, 500)
	o := seedOrder(t, db, 606, 61, 62, consts.OrderCompleted, 300)
	if err := db.Create(&PaymentClearing{
		OrderID: o.ID, BuyerID: o.UserID, SellerID: o.BossID,
		Channel: ChannelWallet, GrossCents: 300, NetCents: 300,
		Currency: "USD", Status: StatusCleared, ClearedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed clearing: %v", err)
	}

	// 预先占用结算 debit 的幂等键，使事务在余额更新后、写流水时失败。
	if err := money.NewLedgerDaoByDB(db).AppendSystemTransaction(
		money.AccountCodeMerchantEscrow,
		o.ID,
		money.DirectionDebit,
		300,
		money.BizTypeOrderSettle,
	); err != nil {
		t.Fatalf("seed conflicting ledger entry: %v", err)
	}

	if err := settleCompletedOrder(db, o.ID); err == nil {
		t.Fatal("settlement should fail when the ledger idempotency key already exists")
	}
	if got := userBalance(t, db, o.BossID); got != 500 {
		t.Fatalf("seller balance must roll back after ledger failure: got %d want 500", got)
	}

	var record PaymentClearing
	if err := db.Where("order_id = ?", o.ID).First(&record).Error; err != nil {
		t.Fatalf("load clearing: %v", err)
	}
	if record.Status != StatusCleared || record.SettledAt != nil {
		t.Fatalf("clearing state must roll back after ledger failure: status=%s settled_at=%v", record.Status, record.SettledAt)
	}
}

func TestExternalClearingUsesSeparateSystemAccounts(t *testing.T) {
	db := openClearingTestDB(t)
	seedUser(t, db, 31, 0)
	seedUser(t, db, 32, 900)
	o := seedOrder(t, db, 202, 31, 32, consts.OrderWaitShip, 450)

	if err := db.Transaction(func(tx *gorm.DB) error {
		return RecordClearedTx(tx, o, ChannelStripe, "pi_123", "usd", nil)
	}); err != nil {
		t.Fatalf("record external clearing: %v", err)
	}

	if got := userBalance(t, db, 32); got != 900 {
		t.Fatalf("external payment credited seller before completion: got %d", got)
	}
	assertLedgerEntry(t, db, o.ID, money.BizTypeOrderClear, money.DirectionDebit, money.AccountCodeExternalClearing, 450)
	assertLedgerEntry(t, db, o.ID, money.BizTypeOrderClear, money.DirectionCredit, money.AccountCodeMerchantEscrow, 450)

	var record PaymentClearing
	if err := db.Where("order_id = ?", o.ID).First(&record).Error; err != nil {
		t.Fatalf("load clearing: %v", err)
	}
	if record.ProviderRef != "pi_123" || record.Channel != ChannelStripe || record.Currency != "USD" {
		t.Fatalf("unexpected external clearing record: %+v", record)
	}
}

func TestLateCompletedEventDoesNotSettleRefundedOrder(t *testing.T) {
	db := openClearingTestDB(t)
	seedUser(t, db, 41, 0)
	seedUser(t, db, 42, 900)
	o := seedOrder(t, db, 303, 41, 42, consts.OrderRefunded, 200)
	if err := db.Create(&PaymentClearing{
		OrderID: o.ID, BuyerID: o.UserID, SellerID: o.BossID,
		Channel: ChannelWallet, GrossCents: 200, NetCents: 200,
		Currency: "USD", Status: StatusCleared, ClearedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed clearing: %v", err)
	}

	if err := settleCompletedOrder(db, o.ID); err != nil {
		t.Fatalf("late completed event should be ignored after refund approval: %v", err)
	}
	if got := userBalance(t, db, o.BossID); got != 900 {
		t.Fatalf("seller received refunded order funds: got %d", got)
	}
}

func TestExternalDuplicatePaymentIsPersistedForRefundReview(t *testing.T) {
	db := openClearingTestDB(t)
	seedUser(t, db, 51, 0)
	seedUser(t, db, 52, 0)
	o := seedOrder(t, db, 404, 51, 52, consts.OrderWaitShip, 500)
	if err := db.Create(&PaymentClearing{
		OrderID: o.ID, BuyerID: o.UserID, SellerID: o.BossID,
		Channel: ChannelStripe, ProviderRef: "cs_original", GrossCents: 500, NetCents: 500,
		Currency: "USD", Status: StatusCleared, ClearedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed clearing: %v", err)
	}

	matched, err := RecordExternalDuplicateTx(db, o, ChannelStripe, "cs_original", "500", "usd")
	if err != nil || !matched {
		t.Fatalf("same provider should be idempotent: matched=%v err=%v", matched, err)
	}
	for i := 0; i < 2; i++ {
		matched, err = RecordExternalDuplicateTx(db, o, ChannelWeb3, "0xduplicate", "5000000", "usdc")
		if err != nil || matched {
			t.Fatalf("duplicate external payment attempt %d: matched=%v err=%v", i+1, matched, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := RecordExternalAnomalyTx(
			db, o, ChannelStripe, "cs_wrong_amount", "1", "eur", AnomalyReasonAmountMismatch,
		); err != nil {
			t.Fatalf("record amount mismatch attempt %d: %v", i+1, err)
		}
	}
	var anomalies []PaymentAnomaly
	if err := db.Find(&anomalies).Error; err != nil {
		t.Fatalf("load anomalies: %v", err)
	}
	if len(anomalies) != 2 {
		t.Fatalf("unexpected anomalies: %+v", anomalies)
	}
	reasons := map[string]bool{}
	for _, anomaly := range anomalies {
		if anomaly.Status != AnomalyStatusPendingReview {
			t.Fatalf("unexpected anomaly status: %+v", anomaly)
		}
		reasons[anomaly.Reason] = true
	}
	if !reasons[AnomalyReasonDuplicatePayment] || !reasons[AnomalyReasonAmountMismatch] {
		t.Fatalf("missing anomaly reason: %+v", anomalies)
	}
}

func TestProviderReplayIsRecognizedFromPersistentClearing(t *testing.T) {
	db := openClearingTestDB(t)
	if err := db.Create(&PaymentClearing{
		OrderID: 505, BuyerID: 1, SellerID: 2, Channel: ChannelWeb3, ProviderRef: "0xabc",
		GrossCents: 100, NetCents: 100, Currency: "USDC", Status: StatusCleared, ClearedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed clearing: %v", err)
	}
	matched, err := isProviderCleared(db, 505, ChannelWeb3, "0xabc")
	if err != nil || !matched {
		t.Fatalf("persistent provider replay not recognized: matched=%v err=%v", matched, err)
	}
	matched, err = isProviderCleared(db, 505, ChannelWeb3, "0xother")
	if err != nil || matched {
		t.Fatalf("different provider must not match: matched=%v err=%v", matched, err)
	}
}

func openClearingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{MoneySecret: "clearing-test-secret"}}
	dsn := fmt.Sprintf("file:clearing-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&user.User{}, &order.Order{}, &money.AccountTransaction{}, &PaymentClearing{}, &PaymentAnomaly{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedUser(t *testing.T, db *gorm.DB, id uint, balance int64) {
	t.Helper()
	u := &user.User{UserName: fmt.Sprintf("u%d", id), Email: fmt.Sprintf("u%d@example.com", id)}
	u.ID = id
	u.Money = strconv.FormatInt(balance, 10)
	cipher, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("encrypt seed balance: %v", err)
	}
	u.Money = cipher
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func seedOrder(t *testing.T, db *gorm.DB, id, buyerID, sellerID, status uint, cents int64) *order.Order {
	t.Helper()
	o := &order.Order{UserID: buyerID, BossID: sellerID, ProductID: 1, AddressID: 1, Num: 1, Money: cents, Type: status}
	o.ID = id
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return o
}

func userBalance(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("load user %d: %v", id, err)
	}
	balance, err := u.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt user %d: %v", id, err)
	}
	return balance
}

func assertLedgerEntry(t *testing.T, db *gorm.DB, orderID uint, bizType, direction, accountCode string, amount int64) {
	t.Helper()
	var entry money.AccountTransaction
	if err := db.Where(
		"ref_order_id = ? AND biz_type = ? AND direction = ?", orderID, bizType, direction,
	).First(&entry).Error; err != nil {
		t.Fatalf("load ledger %s/%s: %v", bizType, direction, err)
	}
	if entry.AccountCode != accountCode || entry.AmountCents != amount {
		t.Fatalf("unexpected ledger entry: %+v", entry)
	}
}

func TestDispatchSettleEventRejectsPoisonMessage(t *testing.T) {
	err := DispatchSettleEvent(context.Background(), "order.completed", []byte(`{"order_id":0}`))
	if !errors.Is(err, errSettlePoisonMessage) {
		t.Fatalf("want poison message, got %v", err)
	}
}
