package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

func TestXcashPaymentServiceCreatesAndReusesInvoice(t *testing.T) {
	db := newXcashTestDB(t, &orderpkg.Order{}, &XcashPaymentIntent{})
	order := &orderpkg.Order{
		UserID: 1, BossID: 2, ProductID: 10, AddressID: 20, Num: 2,
		OrderNum: 20260903001, Type: consts.OrderWaitPay, Money: 1499,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request xcashCreateInvoiceRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"INV260903ABC12345",
			"out_no":%q,
			"currency":"USD",
			"amount":"29.98",
			"pay_url":"https://pay.example.test/pay/INV260903ABC12345",
			"expires_at":"2099-09-03T20:15:00Z",
			"status":"waiting"
		}`, request.OutNo)
	}))
	defer server.Close()

	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
	}, server.Client())
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})

	first, err := service.CreateCheckout(ctx, &XcashCheckoutReq{OrderID: order.ID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateCheckout(ctx, &XcashCheckoutReq{OrderID: order.ID})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Xcash create calls = %d, want 1", calls.Load())
	}
	if first.SysNo != second.SysNo || first.URL != second.URL || first.Amount != "29.98" {
		t.Fatalf("invoice was not reused: first=%+v second=%+v", first, second)
	}
}

func TestXcashPaymentServiceSettlesConfirmedWebhookExactlyOnce(t *testing.T) {
	db := newXcashTestDB(t,
		&user.User{}, &product.Product{}, &orderpkg.Order{},
		&money.AccountTransaction{}, &clearing.PaymentClearing{}, &clearing.PaymentAnomaly{},
		&outbox.OutboxEvent{}, &XcashPaymentIntent{}, &XcashWebhookReceipt{},
	)
	buyer := &user.User{UserName: "buyer", Email: "buyer@example.com"}
	buyer.ID = 1
	if err := db.Create(buyer).Error; err != nil {
		t.Fatal(err)
	}
	item := &product.Product{Name: "keyboard", CategoryID: 1, Num: 5, BossID: 2, BossName: "seller"}
	item.ID = 10
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	order := &orderpkg.Order{
		UserID: 1, BossID: 2, ProductID: item.ID, AddressID: 20, Num: 1,
		OrderNum: 20260903002, Type: consts.OrderWaitPay, Money: 1000,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	intent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: 1, Attempt: 1, OutNo: fmt.Sprintf("gm-%d-1", order.ID),
		SysNo: "INV260903SETTLE01", AmountCents: 1000, Currency: "USD", Status: XcashIntentWaiting,
		PayURL: "https://pay.example.test/pay/INV260903SETTLE01", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}

	client := newXcashClient(xcashConfig{
		BaseURL: "https://pay.example.test", AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods: map[string][]string{"USDC": {"base"}, "USDT": {"tron"}},
	}, nil)
	now := time.Unix(1_788_466_500, 0)
	client.now = func() time.Time { return now }
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	service.commitReservation = func(context.Context, uint, int) {}

	body := []byte(fmt.Sprintf(`{
		"type":"invoice",
		"data":{
			"sys_no":"%s","out_no":"%s","crypto":"USDC","chain":"base",
			"pay_address":"0x1111111111111111111111111111111111111111",
			"pay_amount":"10.00","hash":"0xabc123","block":12345678,"confirmed":true
		}
	}`, intent.SysNo, intent.OutNo))
	headers := xcashWebhookHeaders{
		AppID: "XC-TEST", Nonce: "event-settle-1", Timestamp: fmt.Sprintf("%d", now.Unix()),
	}
	headers.Signature = xcashSignature("secret", headers.Nonce, headers.Timestamp, body)

	if err := service.HandleWebhook(context.Background(), headers, body); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleWebhook(context.Background(), headers, body); err != nil {
		t.Fatalf("webhook replay failed: %v", err)
	}

	var gotOrder orderpkg.Order
	if err := db.First(&gotOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotOrder.Type != consts.OrderWaitShip {
		t.Fatalf("order state = %d, want WaitShip", gotOrder.Type)
	}
	var record clearing.PaymentClearing
	if err := db.Where("order_id = ?", order.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Channel != clearing.ChannelXcash || record.ProviderRef != "base:0xabc123" || record.Currency != "USDC" {
		t.Fatalf("unexpected clearing record: %+v", record)
	}
	var receipts int64
	if err := db.Model(&XcashWebhookReceipt{}).Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("webhook receipts = %d, want 1", receipts)
	}
}

func newXcashTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}
	return db
}
