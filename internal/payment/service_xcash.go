package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
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

const xcashExpiredReconcileWindow = 24 * time.Hour

var ErrXcashInvalidWebhook = errors.New("Xcash Webhook 内容无效")

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
		SysNo                 string `json:"sys_no"`
		OutNo                 string `json:"out_no"`
		Crypto                string `json:"crypto"`
		Chain                 string `json:"chain"`
		PayAddress            string `json:"pay_address"`
		PayAmount             string `json:"pay_amount"`
		Hash                  string `json:"hash"`
		Block                 uint64 `json:"block"`
		Confirmed             bool   `json:"confirmed"`
		RiskLevel             string `json:"risk_level"`
		RiskScore             string `json:"risk_score"`
		Confirmations         uint64 `json:"-"`
		RequiredConfirmations uint64 `json:"-"`
		ConfirmProgress       int    `json:"-"`
		FiatAmountCents       int64  `json:"-"`
		FiatCurrency          string `json:"-"`
		ForcedAnomaly         string `json:"-"`
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
		return fmt.Errorf("%w: JSON 解析失败: %v", ErrXcashInvalidWebhook, err)
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
		return fmt.Errorf("%w: 缺少账单付款字段", ErrXcashInvalidWebhook)
	}
	if !event.Data.Confirmed {
		return fmt.Errorf("%w: 尚未达到确认数", ErrXcashInvalidWebhook)
	}
	invoice, err := s.client.GetInvoice(ctx, event.Data.SysNo)
	if err != nil {
		return err
	}
	if err := mergeCompletedInvoiceIntoEvent(&event, invoice); err != nil {
		return err
	}
	return s.processConfirmedEvent(ctx, &event, &headers)
}

// processConfirmedEvent 收口 Webhook 与主动对账的结算路径。receiptHeaders 仅在 Webhook
// 场景存在；它和清算事务同进同退，使失败的通知仍能由 Xcash 重试。
func (s *XcashPaymentSrv) processConfirmedEvent(ctx context.Context, event *xcashWebhookEvent, receiptHeaders *xcashWebhookHeaders) error {
	providerRef := event.Data.SysNo + ":" + event.Data.Chain + ":" + event.Data.Hash
	db := s.db(ctx)
	if db == nil {
		return errors.New("数据库未初始化")
	}
	var settledProductID uint
	var settledNum int
	err := db.Transaction(func(tx *gorm.DB) error {
		if receiptHeaders != nil {
			receipt := &XcashWebhookReceipt{
				AppID: receiptHeaders.AppID, Nonce: receiptHeaders.Nonce, EventType: event.Type,
				ProviderRef: providerRef, ProcessedAt: time.Now(),
			}
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(receipt)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return nil
			}
		}

		var intent XcashPaymentIntent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("sys_no = ?", event.Data.SysNo).First(&intent).Error; err != nil {
			return err
		}
		if intent.OutNo != event.Data.OutNo {
			event.Data.ForcedAnomaly = clearing.AnomalyReasonPaymentDetailsMismatch
		}
		order, err := orderpkg.NewOrderDaoByDB(tx).GetOrderByIdOnly(intent.OrderID)
		if err != nil {
			return err
		}
		if event.Data.ForcedAnomaly != "" {
			return quarantineXcashPayment(tx, &intent, order, event, providerRef, event.Data.ForcedAnomaly)
		}
		if event.Data.FiatAmountCents != intent.AmountCents || !strings.EqualFold(event.Data.FiatCurrency, intent.Currency) {
			return quarantineXcashPayment(tx, &intent, order, event, providerRef, clearing.AnomalyReasonAmountMismatch)
		}
		if !xcashMethodAllowed(s.client.config.Methods, event.Data.Crypto, event.Data.Chain) {
			return quarantineXcashPayment(tx, &intent, order, event, providerRef, clearing.AnomalyReasonPaymentDetailsMismatch)
		}
		if isXcashHighRisk(event.Data.RiskLevel) {
			return quarantineXcashPayment(tx, &intent, order, event, providerRef, clearing.AnomalyReasonHighRiskPayment)
		}
		if s.client.config.RequireAML && !xcashRiskReady(event.Data.RiskLevel) {
			return tx.Model(&intent).Updates(xcashIntentObservationUpdates(event, XcashIntentRiskPending)).Error
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

		if err := clearing.RecordClearedTx(tx, order, clearing.ChannelXcash, providerRef, intent.Currency, nil); err != nil {
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
			"observed_chain": event.Data.Chain, "observed_crypto": event.Data.Crypto,
			"observed_pay_address": event.Data.PayAddress, "observed_pay_amount": event.Data.PayAmount,
			"confirmations": event.Data.Confirmations, "required_confirmations": event.Data.RequiredConfirmations,
			"confirm_progress": event.Data.ConfirmProgress,
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

func quarantineXcashPayment(tx *gorm.DB, intent *XcashPaymentIntent, order *orderpkg.Order, event *xcashWebhookEvent, providerRef, reason string) error {
	if err := clearing.RecordExternalAnomalyTx(
		tx, order, clearing.ChannelXcash, providerRef, event.Data.PayAmount, event.Data.Crypto, reason,
	); err != nil {
		return err
	}
	return tx.Model(intent).Updates(map[string]any{
		"status": XcashIntentAnomaly, "tx_hash": event.Data.Hash,
		"risk_level": event.Data.RiskLevel, "risk_score": event.Data.RiskScore,
		"observed_chain": event.Data.Chain, "observed_crypto": event.Data.Crypto,
		"observed_pay_address": event.Data.PayAddress, "observed_pay_amount": event.Data.PayAmount,
		"confirmations": event.Data.Confirmations, "required_confirmations": event.Data.RequiredConfirmations,
		"confirm_progress": event.Data.ConfirmProgress,
	}).Error
}

func isXcashHighRisk(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "high", "severe":
		return true
	default:
		return false
	}
}

func xcashRiskReady(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low", "moderate":
		return true
	default:
		return false
	}
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
		ReturnURL: strings.TrimSpace(os.Getenv("XCASH_RETURN_URL")), Currency: xcashFiatCNY,
		Duration: duration, Methods: methods, RequireAML: true,
	}
	var err error
	if raw := strings.TrimSpace(os.Getenv("XCASH_VAULTSLOT_CONFIRMED")); raw != "" {
		config.VaultSlotConfirmed, err = strconv.ParseBool(raw)
		if err != nil {
			return xcashConfig{}, fmt.Errorf("XCASH_VAULTSLOT_CONFIRMED 非法: %w", err)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("XCASH_REQUIRE_AML_RESULT")); raw != "" {
		config.RequireAML, err = strconv.ParseBool(raw)
		if err != nil {
			return xcashConfig{}, fmt.Errorf("XCASH_REQUIRE_AML_RESULT 非法: %w", err)
		}
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
	if req == nil || req.OrderID == 0 {
		return nil, errors.New("订单号不能为空")
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
		current, syncErr := s.GetCheckout(ctx, req)
		if syncErr != nil {
			return nil, syncErr
		}
		if current.Status != XcashIntentExpired {
			return current, nil
		}
		latest.Status = XcashIntentExpired
	case err == nil && latest.Status != XcashIntentExpired:
		return xcashIntentResponse(&latest), nil
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}

	attempt := latest.Attempt + 1
	if attempt == 0 {
		attempt = 1
	}
	outNo := fmt.Sprintf("gm-%s-%s", strconv.FormatUint(uint64(order.ID), 36), strconv.FormatUint(uint64(attempt), 36))
	amount := formatFiatCents(payableCents)
	title := fmt.Sprintf("Gomall #%d", order.OrderNum)
	invoice, err := s.client.CreateInvoice(ctx, outNo, title, amount)
	if err != nil {
		return nil, err
	}
	remoteStatus := strings.ToLower(strings.TrimSpace(invoice.Status))
	switch remoteStatus {
	case "", XcashIntentWaiting, XcashIntentCompleted, XcashIntentExpired:
	default:
		return nil, fmt.Errorf("Xcash 返回未知账单状态 %q", invoice.Status)
	}
	invoiceCents, amountErr := parseFiatCents(invoice.Amount)
	pricingMatches := strings.EqualFold(invoice.Currency, xcashFiatCNY) && amountErr == nil && invoiceCents == payableCents
	methodMatches := invoice.Chain == "" || invoice.Crypto == "" || xcashMethodAllowed(s.client.config.Methods, invoice.Crypto, invoice.Chain)
	if remoteStatus != XcashIntentCompleted && !pricingMatches {
		return nil, errors.New("Xcash 返回的计价金额或币种与订单不一致")
	}
	if remoteStatus != XcashIntentCompleted && !methodMatches {
		return nil, errors.New("Xcash 返回了服务端白名单之外的付款方式")
	}
	expiresAt, err := time.Parse(time.RFC3339, invoice.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("Xcash 账单过期时间非法: %w", err)
	}
	intent := &XcashPaymentIntent{
		OrderID: order.ID, UserID: userInfo.Id, Attempt: attempt, OutNo: outNo, SysNo: invoice.SysNo,
		AmountCents: payableCents, Currency: xcashFiatCNY, Status: remoteStatus,
		Chain: strings.ToLower(invoice.Chain), Crypto: strings.ToUpper(invoice.Crypto), CryptoAddress: invoice.CryptoAddress,
		PayAddress: invoice.PayAddress, PayAmount: invoice.PayAmount, PayURL: invoice.PayURL,
		PaymentURI: invoice.PaymentURI, RiskLevel: invoice.RiskLevel, RiskScore: invoice.RiskScore, ExpiresAt: expiresAt,
	}
	switch remoteStatus {
	case "", XcashIntentWaiting:
		intent.Status = XcashIntentWaiting
	case XcashIntentCompleted:
		// 创建请求可能因网络超时被客户端重试，而 Xcash 会按 out_no 返回已经完成的
		// 原账单。先落成本地 waiting，再走统一主动对账路径，避免只改 intent 而漏掉清算。
		intent.Status = XcashIntentWaiting
	case XcashIntentExpired:
		intent.Status = XcashIntentExpired
	default:
		return nil, fmt.Errorf("Xcash 返回未知账单状态 %q", remoteStatus)
	}
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "order_id"}, {Name: "attempt"}}, DoNothing: true}).Create(intent).Error; err != nil {
		return nil, err
	}
	if intent.ID == 0 {
		if err := db.Where("order_id = ? AND attempt = ?", order.ID, attempt).First(intent).Error; err != nil {
			return nil, err
		}
	}
	if remoteStatus == XcashIntentCompleted {
		return s.GetCheckout(ctx, req)
	}
	return xcashIntentResponse(intent), nil
}

func (s *XcashPaymentSrv) GetCheckout(ctx context.Context, req *XcashCheckoutReq) (*XcashCheckoutResp, error) {
	if s == nil || s.client == nil || s.db == nil {
		return nil, errors.New("Xcash 支付服务未初始化")
	}
	if s.initErr != nil {
		return nil, s.initErr
	}
	if req == nil || req.OrderID == 0 {
		return nil, errors.New("订单号不能为空")
	}
	userInfo, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return nil, err
	}
	db := s.db(ctx)
	if db == nil {
		return nil, errors.New("数据库未初始化")
	}
	var intent XcashPaymentIntent
	if err := db.Where("order_id = ? AND user_id = ?", req.OrderID, userInfo.Id).
		Order("attempt DESC").First(&intent).Error; err != nil {
		return nil, err
	}
	if intent.Status == XcashIntentCompleted || intent.Status == XcashIntentAnomaly {
		return xcashIntentResponse(&intent), nil
	}
	return s.reconcileIntent(ctx, &intent)
}

func (s *XcashPaymentSrv) reconcileIntent(ctx context.Context, intent *XcashPaymentIntent) (*XcashCheckoutResp, error) {
	db := s.db(ctx)
	if db == nil {
		return nil, errors.New("数据库未初始化")
	}
	invoice, err := s.client.GetInvoice(ctx, intent.SysNo)
	if err != nil {
		return nil, err
	}
	invoiceCents, err := parseFiatCents(invoice.Amount)
	pricingMatches := err == nil && strings.EqualFold(invoice.Currency, intent.Currency) && invoiceCents == intent.AmountCents
	expiresAt, err := time.Parse(time.RFC3339, invoice.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("Xcash 账单过期时间非法: %w", err)
	}
	snapshot := inspectXcashInvoice(invoice)

	switch strings.ToLower(strings.TrimSpace(invoice.Status)) {
	case XcashIntentWaiting, XcashIntentExpired:
		if !pricingMatches {
			return nil, errors.New("Xcash 查询结果的计价金额或币种与本地账单不一致")
		}
		if snapshot.detailsMismatch {
			return nil, errors.New("Xcash 查询结果的付款记录与账单不一致")
		}
		if snapshot.chain != "" && snapshot.crypto != "" && !xcashMethodAllowed(s.client.config.Methods, snapshot.crypto, snapshot.chain) {
			return nil, errors.New("Xcash 查询结果包含服务端白名单之外的付款方式")
		}
		if err := db.Model(intent).Updates(map[string]any{
			"status": invoice.Status, "chain": snapshot.chain, "crypto": snapshot.crypto, "crypto_address": invoice.CryptoAddress,
			"pay_address": snapshot.payAddress, "pay_amount": snapshot.payAmount, "pay_url": invoice.PayURL,
			"payment_uri": invoice.PaymentURI, "tx_hash": snapshot.paymentHash,
			"risk_level": invoice.RiskLevel, "risk_score": invoice.RiskScore, "expires_at": expiresAt,
			"confirmations": snapshot.confirmations, "required_confirmations": snapshot.requiredConfirmations,
			"confirm_progress": snapshot.confirmProgress,
		}).Error; err != nil {
			return nil, err
		}
	case XcashIntentCompleted:
		event := &xcashWebhookEvent{Type: "invoice"}
		event.Data.SysNo = intent.SysNo
		event.Data.OutNo = intent.OutNo
		if err := mergeCompletedInvoiceIntoEvent(event, invoice); err != nil {
			return nil, err
		}
		if !pricingMatches {
			event.Data.ForcedAnomaly = clearing.AnomalyReasonAmountMismatch
		}
		if err := s.processConfirmedEvent(ctx, event, nil); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("Xcash 返回未知账单状态 %q", invoice.Status)
	}
	if err := db.First(intent, intent.ID).Error; err != nil {
		return nil, err
	}
	return xcashIntentResponse(intent), nil
}

type xcashInvoiceSnapshot struct {
	chain                 string
	crypto                string
	payAddress            string
	payAmount             string
	paymentHash           string
	block                 uint64
	confirmations         uint64
	requiredConfirmations uint64
	confirmProgress       int
	detailsMismatch       bool
}

func inspectXcashInvoice(invoice *xcashInvoice) xcashInvoiceSnapshot {
	snapshot := xcashInvoiceSnapshot{
		chain:      strings.ToLower(strings.TrimSpace(invoice.Chain)),
		crypto:     strings.ToUpper(strings.TrimSpace(invoice.Crypto)),
		payAddress: strings.TrimSpace(invoice.PayAddress),
		payAmount:  strings.TrimSpace(invoice.PayAmount),
	}
	if invoice.Payment == nil {
		return snapshot
	}
	paymentChain := strings.ToLower(strings.TrimSpace(invoice.Payment.Chain))
	paymentCrypto := strings.ToUpper(strings.TrimSpace(invoice.Payment.Crypto))
	paymentAddress := strings.TrimSpace(invoice.Payment.ToAddress)
	paymentAmount := strings.TrimSpace(invoice.Payment.Amount)
	if (snapshot.chain != "" && paymentChain != "" && !strings.EqualFold(snapshot.chain, paymentChain)) ||
		(snapshot.crypto != "" && paymentCrypto != "" && !strings.EqualFold(snapshot.crypto, paymentCrypto)) ||
		(snapshot.payAddress != "" && paymentAddress != "" && !xcashAddressEqual(paymentChain, snapshot.payAddress, paymentAddress)) ||
		(snapshot.payAmount != "" && paymentAmount != "" && !decimalEqual(snapshot.payAmount, paymentAmount)) {
		snapshot.detailsMismatch = true
	}
	if paymentChain != "" {
		snapshot.chain = paymentChain
	}
	if paymentCrypto != "" {
		snapshot.crypto = paymentCrypto
	}
	if paymentAddress != "" {
		snapshot.payAddress = paymentAddress
	}
	if paymentAmount != "" {
		snapshot.payAmount = paymentAmount
	}
	snapshot.paymentHash = strings.TrimSpace(invoice.Payment.Hash)
	snapshot.block = invoice.Payment.Block
	snapshot.confirmations = invoice.Payment.ConfirmProgress.HasConfirmedCount
	snapshot.requiredConfirmations = invoice.Payment.ConfirmProgress.NeedConfirmedCount
	snapshot.confirmProgress = invoice.Payment.ConfirmProgress.Progress
	return snapshot
}

// mergeCompletedInvoiceIntoEvent 用 HTTPS 查询到的最终账单复核签名 Webhook，或为主动
// 对账构造同一种内部事件。买家在 waiting 阶段可合法换链/换币，所以只比较最终账单。
func mergeCompletedInvoiceIntoEvent(event *xcashWebhookEvent, invoice *xcashInvoice) error {
	if event == nil || invoice == nil || strings.ToLower(strings.TrimSpace(invoice.Status)) != XcashIntentCompleted {
		return errors.New("Xcash 最终账单尚未完成")
	}
	snapshot := inspectXcashInvoice(invoice)
	if invoice.Payment == nil || snapshot.chain == "" || snapshot.crypto == "" || snapshot.payAddress == "" ||
		snapshot.payAmount == "" || snapshot.paymentHash == "" {
		return errors.New("Xcash 已完成账单缺少链上付款记录")
	}
	if snapshot.detailsMismatch ||
		(event.Data.Chain != "" && !strings.EqualFold(event.Data.Chain, snapshot.chain)) ||
		(event.Data.Crypto != "" && !strings.EqualFold(event.Data.Crypto, snapshot.crypto)) ||
		(event.Data.PayAddress != "" && !xcashAddressEqual(snapshot.chain, event.Data.PayAddress, snapshot.payAddress)) ||
		(event.Data.PayAmount != "" && !decimalEqual(event.Data.PayAmount, snapshot.payAmount)) ||
		(event.Data.Hash != "" && !strings.EqualFold(event.Data.Hash, snapshot.paymentHash)) {
		event.Data.ForcedAnomaly = clearing.AnomalyReasonPaymentDetailsMismatch
	}
	event.Data.SysNo = invoice.SysNo
	event.Data.Chain = snapshot.chain
	event.Data.Crypto = snapshot.crypto
	event.Data.PayAddress = snapshot.payAddress
	event.Data.PayAmount = snapshot.payAmount
	event.Data.Hash = snapshot.paymentHash
	event.Data.Block = snapshot.block
	event.Data.Confirmed = true
	if strings.TrimSpace(invoice.RiskLevel) != "" {
		event.Data.RiskLevel = invoice.RiskLevel
	}
	if strings.TrimSpace(invoice.RiskScore) != "" {
		event.Data.RiskScore = invoice.RiskScore
	}
	event.Data.Confirmations = snapshot.confirmations
	event.Data.RequiredConfirmations = snapshot.requiredConfirmations
	event.Data.ConfirmProgress = snapshot.confirmProgress
	event.Data.FiatCurrency = strings.ToUpper(strings.TrimSpace(invoice.Currency))
	invoiceCents, err := parseFiatCents(invoice.Amount)
	if err != nil {
		event.Data.FiatAmountCents = -1
		event.Data.ForcedAnomaly = clearing.AnomalyReasonAmountMismatch
	} else {
		event.Data.FiatAmountCents = invoiceCents
	}
	return nil
}

func xcashAddressEqual(chain, left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "ethereum", "bsc", "polygon", "arbitrum-one", "optimism", "base", "sepolia", "anvil":
		return strings.EqualFold(left, right)
	default:
		// Tron/Nile 使用大小写敏感的 Base58；未来未知链也默认精确比较，避免误放行。
		return left == right
	}
}

func xcashIntentObservationUpdates(event *xcashWebhookEvent, status string) map[string]any {
	return map[string]any{
		"status": status, "chain": event.Data.Chain, "crypto": event.Data.Crypto,
		"pay_address": event.Data.PayAddress, "pay_amount": event.Data.PayAmount, "tx_hash": event.Data.Hash,
		"risk_level": event.Data.RiskLevel, "risk_score": event.Data.RiskScore,
		"observed_chain": event.Data.Chain, "observed_crypto": event.Data.Crypto,
		"observed_pay_address": event.Data.PayAddress, "observed_pay_amount": event.Data.PayAmount,
		"confirmations": event.Data.Confirmations, "required_confirmations": event.Data.RequiredConfirmations,
		"confirm_progress": event.Data.ConfirmProgress,
	}
}

// ReconcilePending 主动查询等待付款、等待风控及最近过期的账单。它不依赖用户请求，
// 供后台定时任务弥补 Webhook 丢失；每张账单按 sys_no 精确复用金额、风险与结算校验。
func (s *XcashPaymentSrv) ReconcilePending(ctx context.Context, limit int) (int, error) {
	if s == nil || s.client == nil || s.db == nil {
		return 0, errors.New("Xcash 支付服务未初始化")
	}
	if s.initErr != nil {
		return 0, s.initErr
	}
	if limit <= 0 {
		limit = 50
	}
	db := s.db(ctx)
	if db == nil {
		return 0, errors.New("数据库未初始化")
	}
	var intents []XcashPaymentIntent
	expiredCutoff := time.Now().Add(-xcashExpiredReconcileWindow)
	if err := db.Where("status IN ? OR (status = ? AND expires_at >= ?)",
		[]string{XcashIntentWaiting, XcashIntentRiskPending}, XcashIntentExpired, expiredCutoff).
		Order("last_checked_at ASC, id ASC").Limit(limit).Find(&intents).Error; err != nil {
		return 0, err
	}
	var errs []error
	for i := range intents {
		intent := &intents[i]
		now := time.Now()
		if err := db.Model(intent).Update("last_checked_at", &now).Error; err != nil {
			errs = append(errs, fmt.Errorf("Xcash 标记对账时间 order=%d sys_no=%s: %w", intent.OrderID, intent.SysNo, err))
			continue
		}
		if _, err := s.reconcileIntent(ctx, intent); err != nil {
			errs = append(errs, fmt.Errorf("Xcash 对账 order=%d sys_no=%s: %w", intent.OrderID, intent.SysNo, err))
		}
	}
	return len(intents), errors.Join(errs...)
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

func parseFiatCents(amount string) (int64, error) {
	value, ok := new(big.Rat).SetString(strings.TrimSpace(amount))
	if !ok || value.Sign() < 0 {
		return 0, errors.New("金额格式非法")
	}
	value.Mul(value, big.NewRat(100, 1))
	if !value.IsInt() || !value.Num().IsInt64() {
		return 0, errors.New("金额必须精确到分")
	}
	return value.Num().Int64(), nil
}

func decimalEqual(left, right string) bool {
	a, ok := new(big.Rat).SetString(strings.TrimSpace(left))
	if !ok {
		return false
	}
	b, ok := new(big.Rat).SetString(strings.TrimSpace(right))
	return ok && a.Cmp(b) == 0
}

func xcashIntentResponse(intent *XcashPaymentIntent) *XcashCheckoutResp {
	return &XcashCheckoutResp{
		SysNo: intent.SysNo, URL: intent.PayURL, Amount: formatFiatCents(intent.AmountCents), Currency: intent.Currency,
		Status: intent.Status, ExpiresAt: intent.ExpiresAt.UTC().Format(time.RFC3339), Chain: intent.Chain,
		Crypto: intent.Crypto, PayAddress: intent.PayAddress, PayAmount: intent.PayAmount,
		PaymentURI: intent.PaymentURI, TxHash: intent.TxHash, RiskLevel: intent.RiskLevel, RiskScore: intent.RiskScore,
		Confirmations: intent.Confirmations, RequiredConfirmations: intent.RequiredConfirmations,
		ConfirmProgress: intent.ConfirmProgress,
	}
}
