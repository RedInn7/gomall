package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/consts"
	orderpkg "github.com/RedInn7/gomall/internal/order"
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
