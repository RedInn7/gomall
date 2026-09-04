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

func TestXcashPaymentServiceQuarantinesMismatchedPayment(t *testing.T) {
	db := newXcashTestDB(t,
		&orderpkg.Order{}, &clearing.PaymentAnomaly{}, &XcashPaymentIntent{}, &XcashWebhookReceipt{},
	)
	order := &orderpkg.Order{
		UserID: 1, BossID: 2, ProductID: 10, AddressID: 20, Num: 1,
		OrderNum: 20260903003, Type: consts.OrderWaitPay, Money: 1000,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	intent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: 1, Attempt: 1, OutNo: fmt.Sprintf("gm-%d-1", order.ID),
		SysNo: "INV260903MISMATCH", AmountCents: 1000, Currency: "USD", Status: XcashIntentWaiting,
		Chain: "base", Crypto: "USDC", PayAddress: "0x1111111111111111111111111111111111111111", PayAmount: "10.00",
		PayURL: "https://pay.example.test/pay/INV260903MISMATCH", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}
	client := newXcashClient(xcashConfig{
		BaseURL: "https://pay.example.test", AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods: map[string][]string{"USDC": {"base"}},
	}, nil)
	now := time.Unix(1_788_466_500, 0)
	client.now = func() time.Time { return now }
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	service.commitReservation = func(context.Context, uint, int) {}

	body := []byte(fmt.Sprintf(`{
		"type":"invoice",
		"data":{
			"sys_no":"%s","out_no":"%s","crypto":"USDC","chain":"base",
			"pay_address":"0x2222222222222222222222222222222222222222",
			"pay_amount":"10.00","hash":"0xmismatch","block":12345678,"confirmed":true
		}
	}`, intent.SysNo, intent.OutNo))
	headers := xcashWebhookHeaders{AppID: "XC-TEST", Nonce: "event-mismatch", Timestamp: fmt.Sprintf("%d", now.Unix())}
	headers.Signature = xcashSignature("secret", headers.Nonce, headers.Timestamp, body)

	if err := service.HandleWebhook(context.Background(), headers, body); err != nil {
		t.Fatalf("deterministic mismatch should be acknowledged after quarantine: %v", err)
	}
	var gotOrder orderpkg.Order
	if err := db.First(&gotOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotOrder.Type != consts.OrderWaitPay {
		t.Fatalf("mismatched payment advanced order to %d", gotOrder.Type)
	}
	var anomaly clearing.PaymentAnomaly
	if err := db.Where("order_id = ?", order.ID).First(&anomaly).Error; err != nil {
		t.Fatal(err)
	}
	if anomaly.Reason != clearing.AnomalyReasonPaymentDetailsMismatch || anomaly.ProviderRef != "base:0xmismatch" {
		t.Fatalf("unexpected anomaly: %+v", anomaly)
	}
	if err := db.First(&intent, intent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if intent.Status != XcashIntentAnomaly {
		t.Fatalf("intent status = %s, want anomaly", intent.Status)
	}
}

func TestXcashPaymentServiceReconcilesCompletedInvoiceWithoutWebhook(t *testing.T) {
	db := newXcashTestDB(t,
		&user.User{}, &product.Product{}, &orderpkg.Order{},
		&money.AccountTransaction{}, &clearing.PaymentClearing{}, &clearing.PaymentAnomaly{},
		&outbox.OutboxEvent{}, &XcashPaymentIntent{}, &XcashWebhookReceipt{},
	)
	buyer := &user.User{UserName: "reconcile-buyer", Email: "reconcile@example.com"}
	buyer.ID = 3
	if err := db.Create(buyer).Error; err != nil {
		t.Fatal(err)
	}
	item := &product.Product{Name: "monitor", CategoryID: 1, Num: 3, BossID: 4, BossName: "seller"}
	item.ID = 30
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	order := &orderpkg.Order{
		UserID: buyer.ID, BossID: 4, ProductID: item.ID, AddressID: 40, Num: 1,
		OrderNum: 20260903004, Type: consts.OrderWaitPay, Money: 2500,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	intent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: buyer.ID, Attempt: 1, OutNo: fmt.Sprintf("gm-%d-1", order.ID),
		SysNo: "INV260903RECONCILE", AmountCents: 2500, Currency: "USD", Status: XcashIntentWaiting,
		PayURL: "https://pay.example.test/pay/INV260903RECONCILE", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"%s","currency":"USD","amount":"25.00","chain":"arbitrum","crypto":"USDT",
			"pay_address":"0x4444444444444444444444444444444444444444","pay_amount":"24.91",
			"pay_url":"https://pay.example.test/pay/%s","expires_at":"2099-09-03T20:15:00Z",
			"status":"completed","risk_level":"Low","risk_score":"10",
			"payment":{"chain":"arbitrum","block":99,"hash":"0xreconciled","from_address":"0xfrom","to_address":"0x4444444444444444444444444444444444444444","crypto":"USDT","amount":"24.910","status":"confirmed",
			"confirm_progress":{"has_confirmed_count":12,"need_confirmed_count":12,"progress":100}}
		}`, intent.SysNo, intent.SysNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods: map[string][]string{"USDT": {"arbitrum"}},
	}, server.Client())
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	service.commitReservation = func(context.Context, uint, int) {}
	processed, err := service.ReconcilePending(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed intents = %d, want 1", processed)
	}
	var gotIntent XcashPaymentIntent
	if err := db.First(&gotIntent, intent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotIntent.Status != XcashIntentCompleted || gotIntent.TxHash != "0xreconciled" || gotIntent.Crypto != "USDT" ||
		gotIntent.Confirmations != 12 || gotIntent.RequiredConfirmations != 12 || gotIntent.ConfirmProgress != 100 {
		t.Fatalf("unexpected reconciled intent: %+v", gotIntent)
	}
	var gotOrder orderpkg.Order
	if err := db.First(&gotOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotOrder.Type != consts.OrderWaitShip {
		t.Fatalf("order state = %d, want WaitShip", gotOrder.Type)
	}
}

func TestXcashPaymentServiceSettlesCompletedIdempotentCreateResponse(t *testing.T) {
	db := newXcashTestDB(t,
		&user.User{}, &product.Product{}, &orderpkg.Order{},
		&money.AccountTransaction{}, &clearing.PaymentClearing{}, &clearing.PaymentAnomaly{},
		&outbox.OutboxEvent{}, &XcashPaymentIntent{}, &XcashWebhookReceipt{},
	)
	buyer := &user.User{UserName: "retry-buyer", Email: "retry@example.com"}
	buyer.ID = 8
	if err := db.Create(buyer).Error; err != nil {
		t.Fatal(err)
	}
	item := &product.Product{Name: "mouse", CategoryID: 1, Num: 4, BossID: 9, BossName: "seller"}
	item.ID = 80
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	order := &orderpkg.Order{
		UserID: buyer.ID, BossID: 9, ProductID: item.ID, AddressID: 90, Num: 1,
		OrderNum: 20260903005, Type: consts.OrderWaitPay, Money: 1800,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		outNo := ""
		if r.Method == http.MethodPost {
			var request xcashCreateInvoiceRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			outNo = request.OutNo
		} else {
			var intent XcashPaymentIntent
			if err := db.Where("order_id = ?", order.ID).First(&intent).Error; err != nil {
				t.Fatal(err)
			}
			outNo = intent.OutNo
		}
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"INV260903CREATEPAID","out_no":%q,"currency":"USD","amount":"18.00",
			"chain":"base","crypto":"DAI","pay_address":"0x8888888888888888888888888888888888888888","pay_amount":"18",
			"pay_url":"https://pay.example.test/pay/INV260903CREATEPAID","expires_at":"2099-09-03T20:15:00Z",
			"status":"completed","risk_level":"Low","risk_score":"5",
			"payment":{"chain":"base","block":100,"hash":"0xcreatepaid","from_address":"0xfrom","to_address":"0x8888888888888888888888888888888888888888","crypto":"DAI","amount":"18.0","status":"confirmed"}
		}`, outNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods: map[string][]string{"DAI": {"base"}},
	}, server.Client())
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	service.commitReservation = func(context.Context, uint, int) {}
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: buyer.ID})

	status, err := service.CreateCheckout(ctx, &XcashCheckoutReq{OrderID: order.ID})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != XcashIntentCompleted || status.Crypto != "DAI" || status.TxHash != "0xcreatepaid" {
		t.Fatalf("completed create was not settled: %+v", status)
	}
	var gotOrder orderpkg.Order
	if err := db.First(&gotOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotOrder.Type != consts.OrderWaitShip {
		t.Fatalf("order state = %d, want WaitShip", gotOrder.Type)
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
