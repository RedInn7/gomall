package payment

import (
	"context"
	"errors"
	"testing"
)

// 未配置密钥时，CreateCheckout 应直接返回 ErrStripeNotConfigured，引导走其它通道。
func TestStripeCheckout_NotConfigured(t *testing.T) {
	t.Setenv(envStripeSecretKey, "")
	_, err := GetStripePaymentSrv().CreateCheckout(context.Background(), &StripeCheckoutReq{OrderID: 1})
	if !errors.Is(err, ErrStripeNotConfigured) {
		t.Fatalf("want ErrStripeNotConfigured, got %v", err)
	}
}

// 未配置 webhook 密钥时，HandleWebhook 应返回 ErrStripeWebhookNotConfigured（handler 据此回 5xx 让重投）。
func TestStripeWebhook_NotConfigured(t *testing.T) {
	t.Setenv(envStripeWebhookSecret, "")
	err := GetStripePaymentSrv().HandleWebhook(context.Background(), []byte(`{}`), "t=1,v1=deadbeef")
	if !errors.Is(err, ErrStripeWebhookNotConfigured) {
		t.Fatalf("want ErrStripeWebhookNotConfigured, got %v", err)
	}
}

// 配了密钥但签名不合法时，必须判定为签名失败（ErrStripeSignature），handler 据此回 400 拒绝伪造请求。
func TestStripeWebhook_BadSignature(t *testing.T) {
	t.Setenv(envStripeWebhookSecret, "whsec_test_secret")
	err := GetStripePaymentSrv().HandleWebhook(context.Background(),
		[]byte(`{"id":"evt_1","type":"checkout.session.completed"}`),
		"t=1,v1=badsignature")
	if !errors.Is(err, ErrStripeSignature) {
		t.Fatalf("want ErrStripeSignature, got %v", err)
	}
}
