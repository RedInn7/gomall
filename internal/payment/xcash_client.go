package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	xcashFiatUSD          = "USD"
	maxXcashBody          = 1 << 20
	xcashWebhookTolerance = 5 * time.Minute
)

var (
	ErrXcashSignature = errors.New("Xcash Webhook 签名无效")
	ErrXcashTimestamp = errors.New("Xcash Webhook 时间戳无效或已过期")
)

type xcashConfig struct {
	BaseURL   string
	AppID     string
	HMACKey   string
	NotifyURL string
	ReturnURL string
	Duration  int
	Methods   map[string][]string
}

func (c xcashConfig) validate() error {
	if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.AppID) == "" || strings.TrimSpace(c.HMACKey) == "" || strings.TrimSpace(c.NotifyURL) == "" {
		return errors.New("Xcash 未配置：需要 XCASH_BASE_URL、XCASH_APP_ID、XCASH_HMAC_KEY 和 XCASH_NOTIFY_URL")
	}
	if _, err := url.ParseRequestURI(c.BaseURL); err != nil {
		return fmt.Errorf("XCASH_BASE_URL 非法: %w", err)
	}
	if c.Duration < 5 || c.Duration > 30 {
		return errors.New("XCASH_INVOICE_DURATION_MINUTES 必须在 5 到 30 之间")
	}
	return nil
}

type xcashCreateInvoiceRequest struct {
	OutNo     string              `json:"out_no"`
	Title     string              `json:"title"`
	Currency  string              `json:"currency"`
	Amount    string              `json:"amount"`
	Duration  int                 `json:"duration"`
	Methods   map[string][]string `json:"methods,omitempty"`
	NotifyURL string              `json:"notify_url"`
	ReturnURL string              `json:"return_url,omitempty"`
}

type xcashPayment struct {
	Chain           string               `json:"chain"`
	Block           uint64               `json:"block"`
	Hash            string               `json:"hash"`
	FromAddress     string               `json:"from_address"`
	ToAddress       string               `json:"to_address"`
	Crypto          string               `json:"crypto"`
	Amount          string               `json:"amount"`
	Status          string               `json:"status"`
	ConfirmProgress xcashConfirmProgress `json:"confirm_progress"`
}

type xcashConfirmProgress struct {
	HasConfirmedCount  uint64 `json:"has_confirmed_count"`
	NeedConfirmedCount uint64 `json:"need_confirmed_count"`
	Progress           int    `json:"progress"`
}

type xcashInvoice struct {
	SysNo         string        `json:"sys_no"`
	OutNo         string        `json:"out_no"`
	Currency      string        `json:"currency"`
	Amount        string        `json:"amount"`
	Chain         string        `json:"chain"`
	Crypto        string        `json:"crypto"`
	CryptoAddress string        `json:"crypto_address"`
	PayAddress    string        `json:"pay_address"`
	PayAmount     string        `json:"pay_amount"`
	PayURL        string        `json:"pay_url"`
	PaymentURI    string        `json:"payment_uri"`
	ExpiresAt     string        `json:"expires_at"`
	Status        string        `json:"status"`
	RiskLevel     string        `json:"risk_level"`
	RiskScore     string        `json:"risk_score"`
	Payment       *xcashPayment `json:"payment"`
}

type xcashClient struct {
	config     xcashConfig
	httpClient *http.Client
	now        func() time.Time
	nonce      func() (string, error)
}

type xcashWebhookHeaders struct {
	AppID     string
	Nonce     string
	Timestamp string
	Signature string
}

func newXcashClient(config xcashConfig, httpClient *http.Client) *xcashClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &xcashClient{
		config:     config,
		httpClient: httpClient,
		now:        time.Now,
		nonce:      xcashNonce,
	}
}

func (c *xcashClient) CreateInvoice(ctx context.Context, outNo, title, amount string) (*xcashInvoice, error) {
	if err := c.config.validate(); err != nil {
		return nil, err
	}
	payload := xcashCreateInvoiceRequest{
		OutNo:     outNo,
		Title:     title,
		Currency:  xcashFiatUSD,
		Amount:    amount,
		Duration:  c.config.Duration,
		Methods:   c.config.Methods,
		NotifyURL: c.config.NotifyURL,
		ReturnURL: c.config.ReturnURL,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := c.signedRequest(ctx, http.MethodPost, "/v1/invoice", body)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("调用 Xcash 创建账单失败: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxXcashBody))
	if err != nil {
		return nil, fmt.Errorf("读取 Xcash 响应失败: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Xcash 创建账单返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var invoice xcashInvoice
	if err := json.Unmarshal(responseBody, &invoice); err != nil {
		return nil, fmt.Errorf("解析 Xcash 账单失败: %w", err)
	}
	if invoice.SysNo == "" || invoice.OutNo != outNo || invoice.PayURL == "" {
		return nil, errors.New("Xcash 创建账单响应缺少必要字段")
	}
	return &invoice, nil
}

func (c *xcashClient) GetInvoice(ctx context.Context, sysNo string) (*xcashInvoice, error) {
	if err := c.config.validate(); err != nil {
		return nil, err
	}
	sysNo = strings.TrimSpace(sysNo)
	if sysNo == "" {
		return nil, errors.New("Xcash 账单号不能为空")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, strings.TrimRight(c.config.BaseURL, "/")+"/v1/invoice/"+url.PathEscape(sysNo), nil,
	)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("查询 Xcash 账单失败: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxXcashBody))
	if err != nil {
		return nil, fmt.Errorf("读取 Xcash 响应失败: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Xcash 查询账单返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var invoice xcashInvoice
	if err := json.Unmarshal(responseBody, &invoice); err != nil {
		return nil, fmt.Errorf("解析 Xcash 账单失败: %w", err)
	}
	if invoice.SysNo != sysNo {
		return nil, errors.New("Xcash 查询响应的账单号不匹配")
	}
	return &invoice, nil
}

func (c *xcashClient) signedRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	nonce, err := c.nonce()
	if err != nil {
		return nil, fmt.Errorf("生成 Xcash nonce 失败: %w", err)
	}
	timestamp := fmt.Sprintf("%d", c.now().Unix())
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.config.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("XC-Appid", c.config.AppID)
	request.Header.Set("XC-Nonce", nonce)
	request.Header.Set("XC-Timestamp", timestamp)
	request.Header.Set("XC-Signature", xcashSignature(c.config.HMACKey, nonce, timestamp, body))
	return request, nil
}

func (c *xcashClient) VerifyWebhook(headers xcashWebhookHeaders, body []byte) error {
	if err := c.config.validate(); err != nil {
		return err
	}
	if headers.AppID != c.config.AppID || headers.Nonce == "" || headers.Timestamp == "" || headers.Signature == "" {
		return ErrXcashSignature
	}
	unixSeconds, err := strconv.ParseInt(headers.Timestamp, 10, 64)
	if err != nil {
		return ErrXcashTimestamp
	}
	age := c.now().Sub(time.Unix(unixSeconds, 0))
	if age < -xcashWebhookTolerance || age > xcashWebhookTolerance {
		return ErrXcashTimestamp
	}
	want := xcashSignature(c.config.HMACKey, headers.Nonce, headers.Timestamp, body)
	if !hmac.Equal([]byte(strings.ToLower(headers.Signature)), []byte(want)) {
		return ErrXcashSignature
	}
	return nil
}

func xcashSignature(secret, nonce, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(nonce))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func xcashNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
