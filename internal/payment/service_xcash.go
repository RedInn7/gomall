package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/consts"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/repository/db/dao"
)

const defaultXcashInvoiceDuration = 15

type xcashDBProvider func(context.Context) *gorm.DB

type XcashPaymentSrv struct {
	client  *xcashClient
	db      xcashDBProvider
	initErr error
}

var (
	xcashPaymentSrvIns  *XcashPaymentSrv
	xcashPaymentSrvOnce sync.Once
)

func GetXcashPaymentSrv() *XcashPaymentSrv {
	xcashPaymentSrvOnce.Do(func() {
		config, err := loadXcashConfig()
		xcashPaymentSrvIns = newXcashPaymentSrv(newXcashClient(config, &http.Client{Timeout: 5 * time.Second}), dao.NewDBClient)
		xcashPaymentSrvIns.initErr = err
	})
	return xcashPaymentSrvIns
}

func newXcashPaymentSrv(client *xcashClient, db xcashDBProvider) *XcashPaymentSrv {
	return &XcashPaymentSrv{client: client, db: db}
}

func loadXcashConfig() (xcashConfig, error) {
	duration := defaultXcashInvoiceDuration
	if raw := strings.TrimSpace(os.Getenv("XCASH_INVOICE_DURATION_MINUTES")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return xcashConfig{}, fmt.Errorf("XCASH_INVOICE_DURATION_MINUTES 非法: %w", err)
		}
		duration = value
	}
	var methods map[string][]string
	if raw := strings.TrimSpace(os.Getenv("XCASH_METHODS_JSON")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &methods); err != nil {
			return xcashConfig{}, fmt.Errorf("XCASH_METHODS_JSON 非法: %w", err)
		}
		methods = normalizeXcashMethods(methods)
		if len(methods) == 0 {
			return xcashConfig{}, errors.New("XCASH_METHODS_JSON 不能是空白名单")
		}
	}
	config := xcashConfig{
		BaseURL: strings.TrimSpace(os.Getenv("XCASH_BASE_URL")), AppID: strings.TrimSpace(os.Getenv("XCASH_APP_ID")),
		HMACKey: strings.TrimSpace(os.Getenv("XCASH_HMAC_KEY")), NotifyURL: strings.TrimSpace(os.Getenv("XCASH_NOTIFY_URL")),
		ReturnURL: strings.TrimSpace(os.Getenv("XCASH_RETURN_URL")), Duration: duration, Methods: methods,
	}
	return config, config.validate()
}

func normalizeXcashMethods(methods map[string][]string) map[string][]string {
	normalized := make(map[string][]string, len(methods))
	for crypto, chains := range methods {
		crypto = strings.ToUpper(strings.TrimSpace(crypto))
		seen := make(map[string]struct{}, len(chains))
		for _, chain := range chains {
			chain = strings.ToLower(strings.TrimSpace(chain))
			if crypto == "" || chain == "" {
				continue
			}
			if _, ok := seen[chain]; ok {
				continue
			}
			seen[chain] = struct{}{}
			normalized[crypto] = append(normalized[crypto], chain)
		}
	}
	return normalized
}

func (s *XcashPaymentSrv) CreateCheckout(ctx context.Context, req *XcashCheckoutReq) (*XcashCheckoutResp, error) {
	if s == nil || s.client == nil || s.db == nil {
		return nil, errors.New("Xcash 支付服务未初始化")
	}
	if s.initErr != nil {
		return nil, s.initErr
	}
	userInfo, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	db := s.db(ctx)
	if db == nil {
		return nil, errors.New("数据库未初始化")
	}
	order, err := orderpkg.NewOrderDaoByDB(db).GetOrderById(req.OrderID, userInfo.Id)
	if err != nil {
		return nil, err
	}
	if order.Type != consts.OrderWaitPay {
		return nil, errors.New("订单状态非未支付")
	}
	payableCents := orderPayableCents(order)
	if payableCents <= 0 {
		return nil, errors.New("订单金额必须大于 0 才能使用 Xcash")
	}

	var latest XcashPaymentIntent
	err = db.Where("order_id = ? AND user_id = ?", order.ID, userInfo.Id).Order("attempt DESC").First(&latest).Error
	switch {
	case err == nil && latest.Status == XcashIntentWaiting && latest.ExpiresAt.After(time.Now()):
		return xcashIntentResponse(&latest), nil
	case err == nil && latest.Status == XcashIntentWaiting:
		if err := db.Model(&latest).Update("status", XcashIntentExpired).Error; err != nil {
			return nil, err
		}
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}

	attempt := latest.Attempt + 1
	if attempt == 0 {
		attempt = 1
	}
	outNo := fmt.Sprintf("gm-%d-%d", order.ID, attempt)
	amount := formatFiatCents(payableCents)
	title := fmt.Sprintf("Gomall order %d", order.OrderNum)
	invoice, err := s.client.CreateInvoice(ctx, outNo, title, amount)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(invoice.Currency, xcashFiatUSD) || invoice.Amount != amount {
		return nil, errors.New("Xcash 返回的计价金额或币种与订单不一致")
	}
	if invoice.Chain != "" && invoice.Crypto != "" && !xcashMethodAllowed(s.client.config.Methods, invoice.Crypto, invoice.Chain) {
		return nil, errors.New("Xcash 返回了服务端白名单之外的付款方式")
	}
	expiresAt, err := time.Parse(time.RFC3339, invoice.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("Xcash 账单过期时间非法: %w", err)
	}
	intent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: userInfo.Id, Attempt: attempt, OutNo: outNo, SysNo: invoice.SysNo,
		AmountCents: payableCents, Currency: strings.ToUpper(invoice.Currency), Status: invoice.Status,
		Chain: strings.ToLower(invoice.Chain), Crypto: strings.ToUpper(invoice.Crypto), CryptoAddress: invoice.CryptoAddress,
		PayAddress: invoice.PayAddress, PayAmount: invoice.PayAmount, PayURL: invoice.PayURL,
		PaymentURI: invoice.PaymentURI, RiskLevel: invoice.RiskLevel, RiskScore: invoice.RiskScore, ExpiresAt: expiresAt,
	}
	if intent.Status == "" {
		intent.Status = XcashIntentWaiting
	}
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "order_id"}, {Name: "attempt"}}, DoNothing: true}).Create(intent).Error; err != nil {
		return nil, err
	}
	if intent.ID == 0 {
		if err := db.Where("order_id = ? AND attempt = ?", order.ID, attempt).First(intent).Error; err != nil {
			return nil, err
		}
	}
	return xcashIntentResponse(intent), nil
}

func xcashMethodAllowed(methods map[string][]string, crypto, chain string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, allowedChain := range methods[strings.ToUpper(strings.TrimSpace(crypto))] {
		if allowedChain == strings.ToLower(strings.TrimSpace(chain)) {
			return true
		}
	}
	return false
}

func formatFiatCents(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func xcashIntentResponse(intent *XcashPaymentIntent) *XcashCheckoutResp {
	return &XcashCheckoutResp{
		SysNo: intent.SysNo, URL: intent.PayURL, Amount: formatFiatCents(intent.AmountCents), Currency: intent.Currency,
		Status: intent.Status, ExpiresAt: intent.ExpiresAt.UTC().Format(time.RFC3339), Chain: intent.Chain,
		Crypto: intent.Crypto, PayAddress: intent.PayAddress, PayAmount: intent.PayAmount,
		PaymentURI: intent.PaymentURI, TxHash: intent.TxHash, RiskLevel: intent.RiskLevel, RiskScore: intent.RiskScore,
	}
}
