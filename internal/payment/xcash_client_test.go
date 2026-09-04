package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestXcashClientCreateInvoiceSignsAndUsesConfiguredMethods(t *testing.T) {
	const secret = "test-hmac-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/invoice" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		message := r.Header.Get("XC-Nonce") + r.Header.Get("XC-Timestamp") + string(body)
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(message))
		wantSignature := hex.EncodeToString(mac.Sum(nil))
		if got := r.Header.Get("XC-Signature"); got != wantSignature {
			t.Fatalf("signature = %q, want %q", got, wantSignature)
		}
		if got := r.Header.Get("XC-Appid"); got != "XC-TEST" {
			t.Fatalf("appid = %q", got)
		}

		var request xcashCreateInvoiceRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.OutNo != "gomall-123" || request.Amount != "29.99" || request.Currency != "CNY" {
			t.Fatalf("unexpected invoice request: %+v", request)
		}
		if len(request.Methods) != 3 || len(request.Methods["USDT"]) != 2 || request.Methods["USDT"][1] != "tron" {
			t.Fatalf("methods must come from the server-side allowlist: %#v", request.Methods)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"sys_no":"INV260903ABC12345",
			"out_no":"gomall-123",
			"currency":"CNY",
			"amount":"29.99",
			"chain":"base",
			"crypto":"USDC",
			"pay_address":"0x1111111111111111111111111111111111111111",
			"pay_amount":"29.99",
			"pay_url":"https://pay.example.test/pay/INV260903ABC12345",
			"expires_at":"2026-09-03T20:15:00Z",
			"status":"waiting"
		}`))
	}))
	defer server.Close()

	client := newXcashClient(xcashConfig{
		BaseURL:            server.URL,
		AppID:              "XC-TEST",
		HMACKey:            secret,
		NotifyURL:          "https://gomall.example.test/api/v1/webhooks/xcash",
		Duration:           15,
		VaultSlotConfirmed: true,
		AllowHTTP:          true,
		Methods: map[string][]string{
			"USDC": {"base", "ethereum"},
			"USDT": {"base", "tron"},
			"ETH":  {"ethereum"},
		},
	}, server.Client())
	client.now = func() time.Time { return time.Unix(1_788_466_500, 0) }
	client.nonce = func() (string, error) { return "fixed-nonce", nil }

	invoice, err := client.CreateInvoice(context.Background(), "gomall-123", "Gomall order 123", "29.99")
	if err != nil {
		t.Fatal(err)
	}
	if invoice.SysNo != "INV260903ABC12345" || invoice.Chain != "base" || invoice.Crypto != "USDC" {
		t.Fatalf("unexpected invoice: %+v", invoice)
	}
}

func TestXcashClientVerifyWebhookRejectsTamperingAndExpiredRequests(t *testing.T) {
	client := newXcashClient(xcashConfig{
		BaseURL:            "https://pay.example.test",
		AppID:              "XC-TEST",
		HMACKey:            "test-hmac-secret",
		NotifyURL:          "https://gomall.example.test/api/v1/webhooks/xcash",
		Duration:           15,
		VaultSlotConfirmed: true,
	}, nil)
	client.now = func() time.Time { return time.Unix(1_788_466_500, 0) }
	body := []byte(`{"type":"invoice","data":{"sys_no":"INV1"}}`)
	timestamp := "1788466500"
	signature := xcashSignature("test-hmac-secret", "event-1", timestamp, body)

	if err := client.VerifyWebhook(xcashWebhookHeaders{
		AppID: "XC-TEST", Nonce: "event-1", Timestamp: timestamp, Signature: signature,
	}, body); err != nil {
		t.Fatalf("valid webhook rejected: %v", err)
	}
	if err := client.VerifyWebhook(xcashWebhookHeaders{
		AppID: "XC-TEST", Nonce: "event-1", Timestamp: timestamp, Signature: signature,
	}, append(body, ' ')); err == nil {
		t.Fatal("tampered webhook must be rejected")
	}
	if err := client.VerifyWebhook(xcashWebhookHeaders{
		AppID: "XC-TEST", Nonce: "event-2", Timestamp: "1788466199",
		Signature: xcashSignature("test-hmac-secret", "event-2", "1788466199", body),
	}, body); err == nil {
		t.Fatal("webhook older than five minutes must be rejected")
	}
}

func TestXcashClientGetInvoiceReturnsPaymentProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/invoice/INV260903QUERY01" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"sys_no":"INV260903QUERY01","currency":"CNY","amount":"50.00",
			"chain":"polygon","crypto":"USDT","pay_address":"0x3333333333333333333333333333333333333333",
			"pay_amount":"49.85","pay_url":"https://pay.example.test/pay/INV260903QUERY01",
			"payment_uri":"ethereum:0x3333333333333333333333333333333333333333",
			"expires_at":"2099-09-03T20:15:00Z","status":"waiting",
			"payment":{"chain":"polygon","block":88,"hash":"0xquery","from_address":"0xfrom","to_address":"0x3333333333333333333333333333333333333333","crypto":"USDT","amount":"49.85","status":"confirming"}
		}`))
	}))
	defer server.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: server.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		VaultSlotConfirmed: true, AllowHTTP: true,
	}, server.Client())

	invoice, err := client.GetInvoice(context.Background(), "INV260903QUERY01")
	if err != nil {
		t.Fatal(err)
	}
	if invoice.SysNo != "INV260903QUERY01" || invoice.Payment == nil || invoice.Payment.Hash != "0xquery" {
		t.Fatalf("unexpected queried invoice: %+v", invoice)
	}
}

func TestXcashClientRequiresHTTPSAndVaultSlotAttestation(t *testing.T) {
	config := xcashConfig{
		BaseURL: "http://pay.example.test", AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Currency: "CNY", Duration: 15,
		VaultSlotConfirmed: true,
	}
	if err := config.validate(); err == nil {
		t.Fatal("plain HTTP Xcash endpoint must be rejected")
	}
	config.BaseURL = "https://pay.example.test"
	config.VaultSlotConfirmed = false
	if err := config.validate(); err == nil {
		t.Fatal("Xcash must fail closed without VaultSlot deployment attestation")
	}
	config.VaultSlotConfirmed = true
	if err := config.validate(); err != nil {
		t.Fatalf("valid secure Xcash config rejected: %v", err)
	}
}

func TestXcashClientDoesNotFollowRedirects(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	client := newXcashClient(xcashConfig{
		BaseURL: redirector.URL, AppID: "XC-TEST", HMACKey: "secret",
		NotifyURL: "https://gomall.example.test/api/v1/webhooks/xcash", Duration: 15,
		VaultSlotConfirmed: true, AllowHTTP: true,
	}, redirector.Client())
	if _, err := client.GetInvoice(context.Background(), "INV260903REDIRECT"); err == nil {
		t.Fatal("redirected invoice query must fail")
	}
	if targetCalled {
		t.Fatal("Xcash client followed a redirect")
	}
}
