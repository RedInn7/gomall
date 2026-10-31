package payment

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

func TestPayDownRejectsDepositPaidPreorder(t *testing.T) {
	db := newPaymentTestDB(t, &orderpkg.Order{})
	o := &orderpkg.Order{
		UserID: 1, BossID: 2, ProductID: 10, AddressID: 20, Num: 1, Money: 1000,
		OrderNum: 20261031001, Type: consts.OrderWaitPay, PreorderStage: 1,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatal(err)
	}
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})

	_, err := GetPaymentSrv().PayDown(ctx, &PaymentDownReq{OrderId: o.ID, Key: "123456"})
	if !errors.Is(err, errPreorderNotPayable) {
		t.Fatalf("paydown on deposit-paid preorder: want errPreorderNotPayable, got %v", err)
	}
	var got orderpkg.Order
	if err := db.First(&got, o.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Type != consts.OrderWaitPay || got.PreorderStage != 1 {
		t.Fatalf("preorder state changed: type=%d stage=%d", got.Type, got.PreorderStage)
	}
}

func TestStripeSettlementRecordsAnomalyWhenStockIsGone(t *testing.T) {
	db := newPaymentTestDB(t, &user.User{}, &product.Product{}, &orderpkg.Order{},
		&money.AccountTransaction{}, &clearing.PaymentClearing{}, &clearing.PaymentAnomaly{}, &outbox.OutboxEvent{})
	item := &product.Product{Name: "keyboard", CategoryID: 1, Num: 0, BossID: 2}
	item.ID = 10
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	o := &orderpkg.Order{
		UserID: 1, BossID: 2, ProductID: item.ID, AddressID: 20, Num: 1, Money: 1000,
		OrderNum: 20261031002, Type: consts.OrderWaitPay,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatal(err)
	}

	// Stripe 已收款、本地库存不足：不能返回错误让 webhook 无限重投；重投也只记一笔异常。
	for i := 0; i < 2; i++ {
		if err := GetStripePaymentSrv().settleStripeOrder(context.Background(), o.ID, o.UserID, "cs_test_oversold", 1000, "cny"); err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}

	var anomalies []clearing.PaymentAnomaly
	if err := db.Find(&anomalies).Error; err != nil {
		t.Fatal(err)
	}
	if len(anomalies) != 1 || anomalies[0].Reason != clearing.AnomalyReasonFulfillmentFailed ||
		anomalies[0].Channel != clearing.ChannelStripe || anomalies[0].ProviderRef != "cs_test_oversold" ||
		anomalies[0].OrderID != o.ID || anomalies[0].Status != clearing.AnomalyStatusPendingReview {
		t.Fatalf("unexpected anomalies: %+v", anomalies)
	}
	var clearings, ledger int64
	if err := db.Model(&clearing.PaymentClearing{}).Count(&clearings).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&money.AccountTransaction{}).Count(&ledger).Error; err != nil {
		t.Fatal(err)
	}
	if clearings != 0 || ledger != 0 {
		t.Fatalf("failed fulfillment must not clear funds: clearings=%d ledger=%d", clearings, ledger)
	}
	var got orderpkg.Order
	if err := db.First(&got, o.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Type != consts.OrderWaitPay {
		t.Fatalf("order state = %d, want WaitPay", got.Type)
	}
}

// newPaymentTestDB 在 newXcashTestDB 之上把库挂成全局 DB（走 dao.NewDBClient 的服务也能用），
// 并补齐余额加密密钥与日志。
func newPaymentTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	db := newXcashTestDB(t, models...)
	previousConf, previousLogger := conf.Config, util.LogrusObj
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{MoneySecret: "payment-test-secret"}}
	util.LogrusObj = logrus.New()
	previousDB := dao.SetTestDB(db)
	t.Cleanup(func() {
		dao.SetTestDB(previousDB)
		conf.Config, util.LogrusObj = previousConf, previousLogger
	})
	return db
}

func TestStripeOnlyChargesCNY(t *testing.T) {
	for _, configured := range []string{"", "cny", " CNY "} {
		t.Setenv(envStripeCurrency, configured)
		got, err := stripeCurrency()
		if err != nil || got != "cny" {
			t.Fatalf("STRIPE_CURRENCY=%q: got %q, %v; want cny", configured, got, err)
		}
	}
	t.Setenv(envStripeCurrency, "usd")
	if _, err := stripeCurrency(); !errors.Is(err, ErrStripeCurrencyUnsupported) {
		t.Fatalf("STRIPE_CURRENCY=usd must be rejected, got %v", err)
	}
}

func TestWeb3AmountConvertsCNYCentsWithConfiguredRate(t *testing.T) {
	t.Setenv(envWeb3PayToken, "usdc")
	t.Setenv(envWeb3USDCDecimals, "6")
	t.Setenv(envWeb3ToleranceBps, "0")
	t.Setenv(envWeb3CentsPerUSDC, "")
	if _, err := expectedTokenBaseUnits(720); !errors.Is(err, ErrWeb3USDCRateNotConfigured) {
		t.Fatalf("USDC without rate must be rejected, got %v", err)
	}

	t.Setenv(envWeb3CentsPerUSDC, "720") // 1 USDC = ¥7.20
	tests := []struct {
		cents int64
		want  string
	}{
		{cents: 1440, want: "2000000"}, // ¥14.40 = 2 USDC
		{cents: 1000, want: "1388888"}, // ¥10.00 = 1.388888… USDC，向下取整到最小单位
	}
	for _, tt := range tests {
		got, err := expectedTokenBaseUnits(tt.cents)
		if err != nil || got.String() != tt.want {
			t.Fatalf("%d cents: got %v, %v; want %s", tt.cents, got, err, tt.want)
		}
	}
	// 按汇率应收 2 USDC（旧逻辑按 1:1 美分会要 14.4 USDC）。足额、超付通过，少付被拒。
	if err := verifyOnchainAmount(1440, "2000000"); err != nil {
		t.Fatalf("exact USDC amount rejected: %v", err)
	}
	if err := verifyOnchainAmount(1440, "14400000"); err != nil {
		t.Fatalf("overpayment rejected: %v", err)
	}
	if err := verifyOnchainAmount(1440, "1999999"); !errors.Is(err, ErrWeb3AmountMismatch) {
		t.Fatalf("underpayment must be rejected, got %v", err)
	}

	t.Setenv(envWeb3PayToken, "eth")
	t.Setenv(envWeb3CentsPerETH, "2160000") // 1 ETH = ¥21600
	got, err := expectedTokenBaseUnits(2160000)
	if err != nil || got.String() != "1000000000000000000" {
		t.Fatalf("ETH conversion: got %v, %v; want 1e18 wei", got, err)
	}
}
