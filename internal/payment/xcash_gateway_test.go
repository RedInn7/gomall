package payment

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

type fakeXcashGateway struct {
	createInvoice func(context.Context, string, string, string) (*xcashInvoice, error)
	getInvoice    func(context.Context, string) (*xcashInvoice, error)
	verifyWebhook func(xcashWebhookHeaders, []byte) error
}

var _ XcashGateway = (*fakeXcashGateway)(nil)

func (f *fakeXcashGateway) CreateInvoice(ctx context.Context, outNo, title, amount string) (*xcashInvoice, error) {
	if f.createInvoice == nil {
		return nil, errors.New("unexpected CreateInvoice call")
	}
	return f.createInvoice(ctx, outNo, title, amount)
}

func (f *fakeXcashGateway) GetInvoice(ctx context.Context, sysNo string) (*xcashInvoice, error) {
	if f.getInvoice == nil {
		return nil, errors.New("unexpected GetInvoice call")
	}
	return f.getInvoice(ctx, sysNo)
}

func (f *fakeXcashGateway) VerifyWebhook(headers xcashWebhookHeaders, body []byte) error {
	if f.verifyWebhook == nil {
		return errors.New("unexpected VerifyWebhook call")
	}
	return f.verifyWebhook(headers, body)
}

func TestXcashPaymentServiceUsesGatewayForWebhookVerificationAndLookup(t *testing.T) {
	lookupErr := errors.New("lookup unavailable")
	verified := false
	gateway := &fakeXcashGateway{
		verifyWebhook: func(headers xcashWebhookHeaders, body []byte) error {
			verified = true
			return nil
		},
		getInvoice: func(ctx context.Context, sysNo string) (*xcashInvoice, error) {
			if sysNo != "INV-GATEWAY-1" {
				t.Fatalf("sys_no = %s, want INV-GATEWAY-1", sysNo)
			}
			return nil, lookupErr
		},
	}
	service := newXcashPaymentSrv(gateway, xcashSettlementPolicy{}, func(context.Context) *gorm.DB {
		t.Fatal("database must not be used when gateway lookup fails")
		return nil
	})
	body := []byte(`{"type":"invoice","data":{"sys_no":"INV-GATEWAY-1","out_no":"gm-1-1","crypto":"USDC","chain":"base","pay_address":"0x1111111111111111111111111111111111111111","pay_amount":"1","hash":"0xhash","confirmed":true}}`)

	err := service.HandleWebhook(context.Background(), xcashWebhookHeaders{}, body)
	if !verified {
		t.Fatal("gateway did not verify webhook")
	}
	if !errors.Is(err, lookupErr) {
		t.Fatalf("HandleWebhook error = %v, want lookup error", err)
	}
}
