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
	"github.com/RedInn7/gomall/internal/clearing"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/repository/db/dao"
)

const defaultXcashInvoiceDuration = 15

type xcashDBProvider func(context.Context) *gorm.DB

type XcashPaymentSrv struct {
	client            *xcashClient
	db                xcashDBProvider
	commitReservation func(context.Context, uint, int)
	initErr           error
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
	return &XcashPaymentSrv{client: client, db: db, commitReservation: commitReservationBestEffort}
}

type xcashWebhookEvent struct {
	Type string `json:"type"`
	Data struct {
		SysNo      string `json:"sys_no"`
		OutNo      string `json:"out_no"`
		Crypto     string `json:"crypto"`
		Chain      string `json:"chain"`
		PayAddress string `json:"pay_address"`
		PayAmount  string `json:"pay_amount"`
		Hash       string `json:"hash"`
		Block      uint64 `json:"block"`
		Confirmed  bool   `json:"confirmed"`
		RiskLevel  string `json:"risk_level"`
		RiskScore  string `json:"risk_score"`
	} `json:"data"`
}

func (s *XcashPaymentSrv) HandleWebhook(ctx context.Context, headers xcashWebhookHeaders, body []byte) error {
	if s == nil || s.client == nil || s.db == nil {
		return errors.New("Xcash 支付服务未初始化")
	}
	if s.initErr != nil {
		return s.initErr
	}
	if err := s.client.VerifyWebhook(headers, body); err != nil {
		return err
	}
	var event xcashWebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return fmt.Errorf("解析 Xcash Webhook 失败: %w", err)
	}
	event.Data.SysNo = strings.TrimSpace(event.Data.SysNo)
	event.Data.OutNo = strings.TrimSpace(event.Data.OutNo)
	event.Data.Chain = strings.ToLower(strings.TrimSpace(event.Data.Chain))
	event.Data.Crypto = strings.ToUpper(strings.TrimSpace(event.Data.Crypto))
	event.Data.Hash = strings.TrimSpace(event.Data.Hash)
	event.Data.PayAddress = strings.TrimSpace(event.Data.PayAddress)
	event.Data.PayAmount = strings.TrimSpace(event.Data.PayAmount)
	if event.Type != "invoice" || event.Data.SysNo == "" || event.Data.OutNo == "" || event.Data.Chain == "" ||
		event.Data.Crypto == "" || event.Data.Hash == "" || event.Data.PayAddress == "" || event.Data.PayAmount == "" {
		return errors.New("Xcash Webhook 缺少账单付款字段")
	}
	if !event.Data.Confirmed {
		return errors.New("Xcash Webhook 尚未达到确认数")
	}
	providerRef := event.Data.Chain + ":" + event.Data.Hash
	db := s.db(ctx)
	if db == nil {
		return errors.New("数据库未初始化")
	}
	var settledProductID uint
	var settledNum int
	err := db.Transaction(func(tx *gorm.DB) error {
		receipt := &XcashWebhookReceipt{
			AppID: headers.AppID, Nonce: headers.Nonce, EventType: event.Type,
			ProviderRef: providerRef, ProcessedAt: time.Now(),
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(receipt)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		var intent XcashPaymentIntent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("sys_no = ? AND out_no = ?", event.Data.SysNo, event.Data.OutNo).First(&intent).Error; err != nil {
			return err
		}
		order, err := orderpkg.NewOrderDaoByDB(tx).GetOrderByIdOnly(intent.OrderID)
		if err != nil {
			return err
		}
		if !xcashMethodAllowed(s.client.config.Methods, event.Data.Crypto, event.Data.Chain) {
			return errors.New("Xcash Webhook 付款方式不在服务端白名单中")
		}
		if (intent.Chain != "" && !strings.EqualFold(intent.Chain, event.Data.Chain)) ||
			(intent.Crypto != "" && !strings.EqualFold(intent.Crypto, event.Data.Crypto)) ||
			(intent.PayAddress != "" && !strings.EqualFold(intent.PayAddress, event.Data.PayAddress)) ||
			(intent.PayAmount != "" && intent.PayAmount != event.Data.PayAmount) {
			return errors.New("Xcash Webhook 与已保存账单不一致")
		}

		if order.Type != consts.OrderWaitPay {
			matched, err := clearing.RecordExternalDuplicateTx(
				tx, order, clearing.ChannelXcash, providerRef, event.Data.PayAmount, event.Data.Crypto,
			)
			if err != nil {
				return err
			}
			status := XcashIntentAnomaly
			if matched {
				status = XcashIntentCompleted
			}
			return tx.Model(&intent).Updates(map[string]any{"status": status, "tx_hash": event.Data.Hash}).Error
		}

		if err := clearing.RecordClearedTx(tx, order, clearing.ChannelXcash, providerRef, event.Data.Crypto, nil); err != nil {
			return err
		}
		if err := finishPaymentConfirmationTx(tx, order); err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&intent).Updates(map[string]any{
			"status": XcashIntentCompleted, "chain": event.Data.Chain, "crypto": event.Data.Crypto,
			"pay_address": event.Data.PayAddress, "pay_amount": event.Data.PayAmount, "tx_hash": event.Data.Hash,
			"risk_level": event.Data.RiskLevel, "risk_score": event.Data.RiskScore, "confirmed_at": &now,
		}).Error; err != nil {
			return err
		}
		settledProductID = order.ProductID
		settledNum = order.Num
		return nil
	})
	if err != nil {
		return err
	}
	if settledProductID != 0 && settledNum > 0 {
		s.commitReservation(ctx, settledProductID, settledNum)
	}
	return nil
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
