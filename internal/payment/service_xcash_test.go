package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
			"currency":"CNY",
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
		VaultSlotConfirmed: true, AllowHTTP: true,
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
		SysNo: "INV260903SETTLE01", AmountCents: 1000, Currency: "CNY", Status: XcashIntentWaiting,
		PayURL: "https://pay.example.test/pay/INV260903SETTLE01", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"%s","currency":"CNY","amount":"10.00","chain":"base","crypto":"USDC",
			"pay_address":"0x1111111111111111111111111111111111111111","pay_amount":"10",
			"pay_url":"https://pay.example.test/pay/%s","expires_at":"2099-09-03T20:15:00Z",
			"status":"completed","risk_level":"Low","risk_score":"10",
			"payment":{"chain":"base","block":12345678,"hash":"0xabc123","from_address":"0xfrom","to_address":"0x1111111111111111111111111111111111111111","crypto":"USDC","amount":"10.00","status":"confirmed"}
		}`, intent.SysNo, intent.SysNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods:            map[string][]string{"USDC": {"base"}, "USDT": {"tron"}},
		VaultSlotConfirmed: true, AllowHTTP: true,
	}, server.Client())
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
	if record.Channel != clearing.ChannelXcash || record.ProviderRef != intent.SysNo+":base:0xabc123" || record.Currency != "CNY" {
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

func TestXcashSignedHighRiskCannotBeDowngradedByCachedQuery(t *testing.T) {
	db := newXcashTestDB(t, &orderpkg.Order{}, &clearing.PaymentAnomaly{}, &XcashPaymentIntent{}, &XcashWebhookReceipt{})
	order := &orderpkg.Order{
		UserID: 31, BossID: 32, ProductID: 33, AddressID: 34, Num: 1,
		OrderNum: 20260903011, Type: consts.OrderWaitPay, Money: 1800,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	intent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: order.UserID, Attempt: 1, OutNo: "gm-risk-merge-1",
		SysNo: "INV260903RISKMERGE", AmountCents: 1800, Currency: "CNY", Status: XcashIntentWaiting,
		PayURL: "https://pay.example.test/pay/INV260903RISKMERGE", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"%s","currency":"CNY","amount":"18.00","chain":"base","crypto":"USDC",
			"pay_address":"0x1818181818181818181818181818181818181818","pay_amount":"18",
			"pay_url":"https://pay.example.test/pay/%s","expires_at":"2099-09-03T20:15:00Z",
			"status":"completed","risk_level":"Low","risk_score":"7",
			"payment":{"chain":"base","block":1818,"hash":"0xriskmerge","from_address":"0xfrom","to_address":"0x1818181818181818181818181818181818181818","crypto":"USDC","amount":"18","status":"confirmed"}
		}`, intent.SysNo, intent.SysNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods: map[string][]string{"USDC": {"base"}}, VaultSlotConfirmed: true, RequireAML: true, AllowHTTP: true,
	}, server.Client())
	now := time.Unix(1_788_466_500, 0)
	client.now = func() time.Time { return now }
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })

	body := []byte(fmt.Sprintf(`{"type":"invoice","data":{"sys_no":"%s","out_no":"%s","crypto":"USDC","chain":"base","pay_address":"0x1818181818181818181818181818181818181818","pay_amount":"18","hash":"0xriskmerge","block":1818,"confirmed":true,"risk_level":"High","risk_score":"91"}}`, intent.SysNo, intent.OutNo))
	headers := xcashWebhookHeaders{AppID: "XC-TEST", Nonce: "event-risk-merge", Timestamp: fmt.Sprintf("%d", now.Unix())}
	headers.Signature = xcashSignature("secret", headers.Nonce, headers.Timestamp, body)
	if err := service.HandleWebhook(context.Background(), headers, body); err != nil {
		t.Fatal(err)
	}

	var gotIntent XcashPaymentIntent
	if err := db.First(&gotIntent, intent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotIntent.Status != XcashIntentAnomaly || gotIntent.RiskLevel != "High" || gotIntent.RiskScore != "91" {
		t.Fatalf("signed high risk was downgraded: %+v", gotIntent)
	}
	var gotOrder orderpkg.Order
	if err := db.First(&gotOrder, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotOrder.Type != consts.OrderWaitPay {
		t.Fatalf("high-risk payment advanced order to %d", gotOrder.Type)
	}
	var anomaly clearing.PaymentAnomaly
	if err := db.Where("order_id = ?", order.ID).First(&anomaly).Error; err != nil {
		t.Fatal(err)
	}
	if anomaly.Reason != clearing.AnomalyReasonHighRiskPayment {
		t.Fatalf("anomaly reason = %s", anomaly.Reason)
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
		SysNo: "INV260903MISMATCH", AmountCents: 1000, Currency: "CNY", Status: XcashIntentWaiting,
		Chain: "base", Crypto: "USDC", PayAddress: "0x1111111111111111111111111111111111111111", PayAmount: "10.00",
		PayURL: "https://pay.example.test/pay/INV260903MISMATCH", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"%s","currency":"CNY","amount":"10.00","chain":"base","crypto":"USDC",
			"pay_address":"0x2222222222222222222222222222222222222222","pay_amount":"10.00",
			"pay_url":"https://pay.example.test/pay/%s","expires_at":"2099-09-03T20:15:00Z",
			"status":"completed","risk_level":"Low","risk_score":"10",
			"payment":{"chain":"base","block":12345678,"hash":"0xmismatch","from_address":"0xfrom","to_address":"0x3333333333333333333333333333333333333333","crypto":"USDC","amount":"10","status":"confirmed"}
		}`, intent.SysNo, intent.SysNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods:            map[string][]string{"USDC": {"base"}},
		VaultSlotConfirmed: true, AllowHTTP: true,
	}, server.Client())
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
	if anomaly.Reason != clearing.AnomalyReasonPaymentDetailsMismatch || anomaly.ProviderRef != intent.SysNo+":base:0xmismatch" {
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
		SysNo: "INV260903RECONCILE", AmountCents: 2500, Currency: "CNY", Status: XcashIntentWaiting,
		Chain: "base", Crypto: "USDC", PayAddress: "0x9999999999999999999999999999999999999999", PayAmount: "25",
		PayURL: "https://pay.example.test/pay/INV260903RECONCILE", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"%s","currency":"CNY","amount":"25.00","chain":"arbitrum-one","crypto":"USDT",
			"pay_address":"0x4444444444444444444444444444444444444444","pay_amount":"24.91",
			"pay_url":"https://pay.example.test/pay/%s","expires_at":"2099-09-03T20:15:00Z",
			"status":"completed","risk_level":"Low","risk_score":"10",
			"payment":{"chain":"arbitrum-one","block":99,"hash":"0xreconciled","from_address":"0xfrom","to_address":"0x4444444444444444444444444444444444444444","crypto":"USDT","amount":"24.910","status":"confirmed",
			"confirm_progress":{"has_confirmed_count":12,"need_confirmed_count":12,"progress":100}}
		}`, intent.SysNo, intent.SysNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods: map[string][]string{"USDT": {"arbitrum-one"}}, VaultSlotConfirmed: true, AllowHTTP: true,
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
			"sys_no":"INV260903CREATEPAID","out_no":%q,"currency":"CNY","amount":"18.00",
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
		Methods: map[string][]string{"DAI": {"base"}}, VaultSlotConfirmed: true, AllowHTTP: true,
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

func TestXcashPaymentServiceWaitsForAMLThenQuarantinesHighRisk(t *testing.T) {
	db := newXcashTestDB(t, &orderpkg.Order{}, &clearing.PaymentAnomaly{}, &XcashPaymentIntent{})
	order := &orderpkg.Order{
		UserID: 11, BossID: 12, ProductID: 110, AddressID: 120, Num: 1,
		OrderNum: 20260903006, Type: consts.OrderWaitPay, Money: 3000,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	intent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: order.UserID, Attempt: 1, OutNo: "gm-aml-1",
		SysNo: "INV260903AMLPEND1", AmountCents: 3000, Currency: "CNY", Status: XcashIntentWaiting,
		PayURL: "https://pay.example.test/pay/INV260903AMLPEND1", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		riskFields := `"risk_level":null,"risk_score":null`
		if calls.Add(1) > 1 {
			riskFields = `"risk_level":"High","risk_score":"88"`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"%s","currency":"CNY","amount":"30.00","chain":"base","crypto":"USDC",
			"pay_address":"0x1111111111111111111111111111111111111111","pay_amount":"30",
			"pay_url":"https://pay.example.test/pay/%s","expires_at":"2099-09-03T20:15:00Z",
			"status":"completed",%s,
			"payment":{"chain":"base","block":101,"hash":"0xamlhigh","from_address":"0xfrom","to_address":"0x1111111111111111111111111111111111111111","crypto":"USDC","amount":"30.0","status":"confirmed"}
		}`, intent.SysNo, intent.SysNo, riskFields)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods: map[string][]string{"USDC": {"base"}}, VaultSlotConfirmed: true, RequireAML: true, AllowHTTP: true,
	}, server.Client())
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	service.commitReservation = func(context.Context, uint, int) {}
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: order.UserID})

	first, err := service.GetCheckout(ctx, &XcashCheckoutReq{OrderID: order.ID})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != XcashIntentRiskPending {
		t.Fatalf("first status = %s, want risk_pending", first.Status)
	}
	var before orderpkg.Order
	if err := db.First(&before, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if before.Type != consts.OrderWaitPay {
		t.Fatalf("risk pending payment advanced order to %d", before.Type)
	}
	second, err := service.GetCheckout(ctx, &XcashCheckoutReq{OrderID: order.ID})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != XcashIntentAnomaly || second.RiskLevel != "High" {
		t.Fatalf("high risk payment was not quarantined: %+v", second)
	}
	var anomaly clearing.PaymentAnomaly
	if err := db.Where("order_id = ?", order.ID).First(&anomaly).Error; err != nil {
		t.Fatal(err)
	}
	if anomaly.Reason != clearing.AnomalyReasonHighRiskPayment {
		t.Fatalf("anomaly reason = %s", anomaly.Reason)
	}

	// 模拟高风险结果已经隔离后，一个更早发出的 Low 查询才返回。旧结果不能重新
	// 结算，更不能推进订单状态。
	staleLow := &xcashWebhookEvent{Type: "invoice"}
	staleLow.Data.SysNo = intent.SysNo
	staleLow.Data.OutNo = intent.OutNo
	staleLow.Data.Chain = "base"
	staleLow.Data.Crypto = "USDC"
	staleLow.Data.PayAddress = "0x1111111111111111111111111111111111111111"
	staleLow.Data.PayAmount = "30"
	staleLow.Data.Hash = "0xamlhigh"
	staleLow.Data.Confirmed = true
	staleLow.Data.RiskLevel = "Low"
	staleLow.Data.FiatAmountCents = intent.AmountCents
	staleLow.Data.FiatCurrency = intent.Currency
	if err := service.processConfirmedEvent(ctx, staleLow, nil); err != nil {
		t.Fatal(err)
	}
	var after orderpkg.Order
	if err := db.First(&after, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Type != consts.OrderWaitPay {
		t.Fatalf("stale low-risk result advanced quarantined order to %d", after.Type)
	}
	var anomalyCount int64
	if err := db.Model(&clearing.PaymentAnomaly{}).Where("order_id = ?", order.ID).Count(&anomalyCount).Error; err != nil {
		t.Fatal(err)
	}
	if anomalyCount != 1 {
		t.Fatalf("anomaly count = %d, want 1", anomalyCount)
	}
}

func TestXcashStaleWaitingSnapshotDoesNotRevertCompletedIntent(t *testing.T) {
	db := newXcashTestDB(t, &XcashPaymentIntent{})
	intent := &XcashPaymentIntent{
		OrderID: 301, UserID: 302, Attempt: 1, OutNo: "gm-monotonic-1",
		SysNo: "INV260903MONOTONIC1", AmountCents: 100, Currency: "CNY", Status: XcashIntentCompleted,
		PayURL: "https://pay.example.test/pay/INV260903MONOTONIC1", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(intent).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"sys_no":%q,"currency":"CNY","amount":"1.00","pay_url":"https://pay.example.test/pay/monotonic","expires_at":"2099-09-03T20:15:00Z","status":"waiting"}`, intent.SysNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		VaultSlotConfirmed: true, AllowHTTP: true,
	}, server.Client())
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	stale := *intent
	stale.Status = XcashIntentWaiting

	status, err := service.reconcileIntent(context.Background(), &stale)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != XcashIntentCompleted {
		t.Fatalf("stale waiting snapshot reverted status to %s", status.Status)
	}
}

func TestXcashPaymentServiceReconcilesRecentExpiredAttemptBySysNo(t *testing.T) {
	db := newXcashTestDB(t,
		&user.User{}, &product.Product{}, &orderpkg.Order{},
		&money.AccountTransaction{}, &clearing.PaymentClearing{}, &clearing.PaymentAnomaly{},
		&outbox.OutboxEvent{}, &XcashPaymentIntent{},
	)
	buyer := &user.User{UserName: "late-buyer", Email: "late@example.com"}
	buyer.ID = 21
	if err := db.Create(buyer).Error; err != nil {
		t.Fatal(err)
	}
	item := &product.Product{Name: "dock", CategoryID: 1, Num: 2, BossID: 22, BossName: "seller"}
	item.ID = 210
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	order := &orderpkg.Order{
		UserID: buyer.ID, BossID: 22, ProductID: item.ID, AddressID: 220, Num: 1,
		OrderNum: 20260903007, Type: consts.OrderWaitPay, Money: 4200,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	oldIntent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: buyer.ID, Attempt: 1, OutNo: "gm-late-1", SysNo: "INV260903LATEOLD1",
		AmountCents: 4200, Currency: "CNY", Status: XcashIntentExpired,
		PayURL: "https://pay.example.test/pay/INV260903LATEOLD1", ExpiresAt: time.Now().Add(-time.Minute),
	}
	latestIntent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: buyer.ID, Attempt: 2, OutNo: "gm-late-2", SysNo: "INV260903LATENEW2",
		AmountCents: 4200, Currency: "CNY", Status: XcashIntentWaiting,
		PayURL: "https://pay.example.test/pay/INV260903LATENEW2", ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	if err := db.Create(oldIntent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(latestIntent).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"sys_no":"%s","currency":"CNY","amount":"42.00","chain":"base","crypto":"DAI",
			"pay_address":"0x2121212121212121212121212121212121212121","pay_amount":"42",
			"pay_url":"https://pay.example.test/pay/%s","expires_at":"2026-09-03T20:15:00Z",
			"status":"completed","risk_level":"Low","risk_score":"4",
			"payment":{"chain":"base","block":102,"hash":"0xlateold","from_address":"0xfrom","to_address":"0x2121212121212121212121212121212121212121","crypto":"DAI","amount":"42","status":"confirmed"}
		}`, oldIntent.SysNo, oldIntent.SysNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		Methods: map[string][]string{"DAI": {"base"}}, VaultSlotConfirmed: true, AllowHTTP: true,
	}, server.Client())
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	service.commitReservation = func(context.Context, uint, int) {}

	processed, err := service.ReconcilePending(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if err := db.First(oldIntent, oldIntent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if oldIntent.Status != XcashIntentCompleted {
		t.Fatalf("old intent status = %s, want completed", oldIntent.Status)
	}
	if err := db.First(latestIntent, latestIntent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if latestIntent.Status != XcashIntentWaiting {
		t.Fatalf("latest intent unexpectedly changed to %s", latestIntent.Status)
	}
}

func TestXcashPaymentServiceRotatesReconcileBatch(t *testing.T) {
	db := newXcashTestDB(t, &XcashPaymentIntent{})
	for i := 1; i <= 2; i++ {
		intent := &XcashPaymentIntent{
			OrderID: uint(i), UserID: 1, Attempt: 1, OutNo: fmt.Sprintf("gm-rotate-%d", i),
			SysNo: fmt.Sprintf("INV260903ROTATE%02d", i), AmountCents: 100, Currency: "CNY", Status: XcashIntentWaiting,
			PayURL: "https://pay.example.test/pay/rotate", ExpiresAt: time.Now().Add(15 * time.Minute),
		}
		if err := db.Create(intent).Error; err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sysNo := strings.TrimPrefix(r.URL.Path, "/v1/invoice/")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"sys_no":%q,"currency":"CNY","amount":"1.00","pay_url":"https://pay.example.test/pay/rotate","expires_at":"2099-09-03T20:15:00Z","status":"waiting"}`, sysNo)
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		VaultSlotConfirmed: true, AllowHTTP: true,
	}, server.Client())
	service := newXcashPaymentSrv(client, func(ctx context.Context) *gorm.DB { return db.WithContext(ctx) })
	if _, err := service.ReconcilePending(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcilePending(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var intents []XcashPaymentIntent
	if err := db.Order("id ASC").Find(&intents).Error; err != nil {
		t.Fatal(err)
	}
	if intents[0].LastCheckedAt == nil || intents[1].LastCheckedAt == nil {
		t.Fatalf("reconcile batch did not rotate: %+v", intents)
	}
}

func TestXcashAddressComparisonRespectsChainEncoding(t *testing.T) {
	for _, chain := range []string{"base", "avalanche", "linea", "scroll"} {
		if !xcashAddressEqual(chain, "0xAbC", "0xaBc") {
			t.Fatalf("%s address comparison should ignore hex case", chain)
		}
	}
	if xcashAddressEqual("tron", "TAbc", "TaBc") {
		t.Fatal("Tron Base58 address comparison must be case-sensitive")
	}
}

func TestMergeCompletedInvoiceRequiresConfirmedPaymentFields(t *testing.T) {
	valid := &xcashInvoice{
		SysNo: "INV260903COMPLETE", Currency: "CNY", Amount: "1.00", Status: XcashIntentCompleted,
		Chain: "base", Crypto: "USDC", PayAddress: "0xabc", PayAmount: "1",
		Payment: &xcashPayment{
			Chain: "base", Crypto: "USDC", ToAddress: "0xabc", Amount: "1", Hash: "0xhash", Status: "confirmed",
		},
	}
	if err := mergeCompletedInvoiceIntoEvent(&xcashWebhookEvent{}, valid); err != nil {
		t.Fatalf("valid completed invoice rejected: %v", err)
	}

	for name, mutate := range map[string]func(*xcashInvoice){
		"missing chain":   func(invoice *xcashInvoice) { invoice.Payment.Chain = "" },
		"missing crypto":  func(invoice *xcashInvoice) { invoice.Payment.Crypto = "" },
		"missing address": func(invoice *xcashInvoice) { invoice.Payment.ToAddress = "" },
		"missing amount":  func(invoice *xcashInvoice) { invoice.Payment.Amount = "" },
		"missing hash":    func(invoice *xcashInvoice) { invoice.Payment.Hash = "" },
		"unconfirmed":     func(invoice *xcashInvoice) { invoice.Payment.Status = "confirming" },
	} {
		t.Run(name, func(t *testing.T) {
			invoice := *valid
			payment := *valid.Payment
			invoice.Payment = &payment
			mutate(&invoice)
			if err := mergeCompletedInvoiceIntoEvent(&xcashWebhookEvent{}, &invoice); err == nil {
				t.Fatal("incomplete payment was accepted")
			}
		})
	}
}

func TestXcashRiskMergeKeepsSignedScoreAtSameLevel(t *testing.T) {
	level, score := stricterXcashRiskSnapshot("High", "91", "High", "7")
	if level != "High" || score != "91" {
		t.Fatalf("same-level cached risk replaced signed evidence: level=%s score=%s", level, score)
	}
}

func TestLoadXcashConfigUsesCNYStrictAMLAndNormalizesMethods(t *testing.T) {
	t.Setenv("XCASH_BASE_URL", "https://pay.example.test")
	t.Setenv("XCASH_APP_ID", "XC-TEST")
	t.Setenv("XCASH_HMAC_KEY", "secret")
	t.Setenv("XCASH_NOTIFY_URL", "https://gomall.example.test/api/v1/webhooks/xcash")
	t.Setenv("XCASH_VAULTSLOT_CONFIRMED", "true")
	t.Setenv("XCASH_METHODS_JSON", `{"usdc":[" Base ","base"],"USDT":["tron"]}`)
	t.Setenv("XCASH_REQUIRE_AML_RESULT", "")

	config, err := loadXcashConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Currency != "CNY" || !config.RequireAML || !config.VaultSlotConfirmed {
		t.Fatalf("unsafe defaults: %+v", config)
	}
	if len(config.Methods["USDC"]) != 1 || config.Methods["USDC"][0] != "base" {
		t.Fatalf("methods were not normalized: %#v", config.Methods)
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
