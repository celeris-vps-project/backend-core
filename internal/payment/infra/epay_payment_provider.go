package infra

import (
	"backend-core/internal/payment/domain"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var epayHTTPClient = &http.Client{Timeout: 30 * time.Second}

// EPayPaymentProvider implements the common EPay v1 submit.php flow.
type EPayPaymentProvider struct {
	providerID string
	config     EPayProviderConfig
	charges    sync.Map
	seq        atomic.Int64
	callback   func(payload *domain.WebhookPayload)
}

// EPayProviderConfig holds the parsed v1-compatible EPay configuration.
type EPayProviderConfig struct {
	APIURL           string
	PID              string
	MerchantKey      string
	PayType          string
	NotifyURL        string
	ReturnURL        string
	ProductName      string
	MockMode         bool
	MockConfirmDelay time.Duration
}

type epayChargeRecord struct {
	ChargeID string
	OrderID  string
	Amount   int64
	Currency string
	Status   string
}

func NewEPayPaymentProvider(
	cfg *domain.PaymentProviderConfig,
	onWebhook func(payload *domain.WebhookPayload),
) *EPayPaymentProvider {
	return &EPayPaymentProvider{
		providerID: cfg.ID,
		config:     parseEPayConfig(cfg),
		callback:   onWebhook,
	}
}

func parseEPayConfig(cfg *domain.PaymentProviderConfig) EPayProviderConfig {
	c := EPayProviderConfig{
		ProductName:      "VPS Service",
		MockConfirmDelay: 3 * time.Second,
	}
	if cfg.Config == nil {
		return c
	}
	if v, ok := cfg.Config["api_url"].(string); ok {
		c.APIURL = strings.TrimRight(strings.TrimSpace(v), "/")
	}
	if v, ok := cfg.Config["pid"].(string); ok {
		c.PID = strings.TrimSpace(v)
	}
	if v, ok := cfg.Config["merchant_key"].(string); ok {
		c.MerchantKey = v
	}
	if v, ok := cfg.Config["pay_type"].(string); ok {
		c.PayType = strings.TrimSpace(v)
	}
	if v, ok := cfg.Config["notify_url"].(string); ok {
		c.NotifyURL = strings.TrimSpace(v)
	}
	if v, ok := cfg.Config["return_url"].(string); ok {
		c.ReturnURL = strings.TrimSpace(v)
	}
	if v, ok := cfg.Config["product_name"].(string); ok && strings.TrimSpace(v) != "" {
		c.ProductName = strings.TrimSpace(v)
	}
	if v, ok := cfg.Config["mock_mode"].(bool); ok {
		c.MockMode = v
	}
	if v, ok := cfg.Config["mock_confirm_delay"].(string); ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.MockConfirmDelay = d
		}
	}
	return c
}

func (p *EPayPaymentProvider) SetCallback(cb func(payload *domain.WebhookPayload)) {
	p.callback = cb
}

func (p *EPayPaymentProvider) CreateCharge(ctx context.Context, orderID string, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	chargeID := fmt.Sprintf("epay_%s_%d", shortEPayID(p.providerID), p.seq.Add(1))

	rec := &epayChargeRecord{
		ChargeID: chargeID,
		OrderID:  orderID,
		Amount:   amountMinor,
		Currency: currency,
		Status:   domain.ChargeStatusPending,
	}
	p.charges.Store(chargeID, rec)

	money := fmt.Sprintf("%.2f", float64(amountMinor)/100.0)
	log.Printf("[EPayProvider:%s] charge creating: id=%s order=%s %s amount=%s",
		shortEPayID(p.providerID), chargeID, orderID, currency, money)

	var paymentURL string
	var err error
	if p.config.MockMode {
		paymentURL = fmt.Sprintf("/orders/%s/payments/status", orderID)
	} else {
		paymentURL, err = p.createChargeV1(ctx, orderID, money)
	}
	if err != nil {
		return nil, fmt.Errorf("EPay charge creation failed: %w", err)
	}

	result := &domain.ChargeResult{
		ChargeID:   chargeID,
		Status:     domain.ChargeStatusPending,
		PaymentURL: paymentURL,
	}

	log.Printf("[EPayProvider:%s] charge created: id=%s order=%s pay_url=%s",
		shortEPayID(p.providerID), chargeID, orderID, paymentURL)

	if p.config.MockMode && p.callback != nil {
		go func() {
			time.Sleep(p.config.MockConfirmDelay)
			log.Printf("[EPayProvider:%s] MOCK: auto-confirming charge=%s order=%s",
				shortEPayID(p.providerID), chargeID, orderID)
			rec.Status = domain.ChargeStatusSuccess
			p.callback(&domain.WebhookPayload{
				ChargeID:  chargeID,
				OrderID:   orderID,
				Status:    domain.ChargeStatusSuccess,
				RawBody:   []byte(fmt.Sprintf("out_trade_no=%s&trade_no=%s&status=1", orderID, chargeID)),
				Signature: "mock_epay_signature",
			})
		}()
	}

	return result, nil
}

func (p *EPayPaymentProvider) createChargeV1(ctx context.Context, orderID, money string) (string, error) {
	if p.config.APIURL == "" {
		return "", fmt.Errorf("api_url is required")
	}
	if p.config.PID == "" {
		return "", fmt.Errorf("pid is required")
	}
	if p.config.MerchantKey == "" {
		return "", fmt.Errorf("merchant_key is required")
	}
	if p.config.PayType == "" {
		return "", fmt.Errorf("pay_type is required")
	}
	if p.config.NotifyURL == "" {
		return "", fmt.Errorf("notify_url is required")
	}
	if p.config.ReturnURL == "" {
		return "", fmt.Errorf("return_url is required")
	}

	returnURL := strings.ReplaceAll(p.config.ReturnURL, "{order_id}", orderID)
	productName := strings.ReplaceAll(p.config.ProductName, "{order_id}", orderID)
	params := map[string]string{
		"pid":          p.config.PID,
		"type":         p.config.PayType,
		"out_trade_no": orderID,
		"notify_url":   p.config.NotifyURL,
		"return_url":   returnURL,
		"name":         productName,
		"money":        money,
	}
	params["sign"] = p.computeSignV1(params)
	params["sign_type"] = "MD5"

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	submitURL := p.config.APIURL + "/submit.php"
	client := *epayHTTPClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, submitURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return "", readErr
	}
	if res.StatusCode != http.StatusFound {
		return "", fmt.Errorf("submit.php expected 302, got %d: %s", res.StatusCode, string(body))
	}

	location := strings.TrimSpace(res.Header.Get("Location"))
	if location == "" {
		return "", fmt.Errorf("submit.php returned 302 without Location")
	}

	log.Printf("[EPayProvider:%s] v1 submit accepted: order=%s type=%s",
		shortEPayID(p.providerID), orderID, p.config.PayType)
	return location, nil
}

// VerifyWebhook validates a v1-compatible EPay form callback.
func (p *EPayPaymentProvider) VerifyWebhook(rawBody []byte, headers domain.WebhookHeaders) (*domain.WebhookPayload, error) {
	_ = headers
	body := strings.TrimSpace(string(rawBody))
	if body == "" {
		return nil, fmt.Errorf("empty webhook body")
	}

	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, fmt.Errorf("invalid form webhook body: %w", err)
	}
	signature := strings.TrimSpace(values.Get("sign"))
	if signature == "" {
		return nil, fmt.Errorf("missing EPay sign")
	}
	if signType := strings.TrimSpace(values.Get("sign_type")); signType != "" && !strings.EqualFold(signType, "MD5") {
		return nil, fmt.Errorf("unsupported EPay sign_type: %s", signType)
	}
	if err := p.verifySignV1(values, signature); err != nil {
		return nil, err
	}

	payload, err := p.parseFormWebhook(values)
	if err != nil {
		return nil, err
	}
	payload.RawBody = rawBody
	payload.Signature = signature

	p.updateChargeStatus(payload.OrderID, payload.Status)
	log.Printf("[EPayProvider:%s] webhook received: charge=%s order=%s status=%s",
		shortEPayID(p.providerID), payload.ChargeID, payload.OrderID, payload.Status)
	return payload, nil
}

func (p *EPayPaymentProvider) verifySignV1(values url.Values, actual string) error {
	expected := p.computeSignV1(formValuesToMap(values))
	actual = strings.ToLower(strings.TrimSpace(actual))
	if !hmac.Equal([]byte(expected), []byte(actual)) {
		return fmt.Errorf("webhook signature mismatch")
	}
	return nil
}

func (p *EPayPaymentProvider) parseFormWebhook(values url.Values) (*domain.WebhookPayload, error) {
	orderID := firstFormValue(values, "out_trade_no", "merchant_order_id", "merchantOrderNo", "order_id")
	if orderID == "" {
		return nil, fmt.Errorf("webhook missing out_trade_no")
	}

	status, err := normalizeEPayWebhookStatus(firstFormValue(values, "status", "trade_status", "result_code"))
	if err != nil {
		return nil, err
	}

	chargeID := firstFormValue(values, "trade_no", "epayOrderNo", "epay_order_no", "id")
	if chargeID == "" {
		chargeID = orderID
	}
	return &domain.WebhookPayload{
		ChargeID: chargeID,
		OrderID:  orderID,
		Status:   status,
	}, nil
}

func (p *EPayPaymentProvider) updateChargeStatus(orderID, status string) {
	p.charges.Range(func(key, value interface{}) bool {
		r := value.(*epayChargeRecord)
		if r.OrderID == orderID {
			r.Status = status
			return false
		}
		return true
	})
}

func (p *EPayPaymentProvider) computeSignV1(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if strings.EqualFold(k, "sign") || strings.EqualFold(k, "sign_type") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		if params[k] == "" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, params[k]))
	}

	hash := md5.Sum([]byte(strings.Join(pairs, "&") + p.config.MerchantKey))
	return strings.ToLower(hex.EncodeToString(hash[:]))
}

func formValuesToMap(values url.Values) map[string]string {
	params := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) == 0 {
			continue
		}
		params[k] = v[0]
	}
	return params
}

func firstFormValue(values url.Values, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(values.Get(key)); v != "" {
			return v
		}
	}
	return ""
}

func normalizeEPayWebhookStatus(raw string) (string, error) {
	status := strings.ToUpper(strings.TrimSpace(raw))
	switch status {
	case "1", "7", "SUCCESS", "SUCCEEDED", "TRADE_SUCCESS", "TRADE_FINISHED", "PAY_SUCCESS", "PAID":
		return domain.ChargeStatusSuccess, nil
	case "0", "PENDING", "WAIT", "WAIT_BUYER_PAY", "PROCESSING":
		return domain.ChargeStatusPending, nil
	case "5", "6", "8", "20", "FAIL", "FAILED", "CANCEL", "CANCELED", "CANCELLED", "CLOSE", "CLOSED", "TRADE_CLOSED":
		return domain.ChargeStatusFailed, nil
	default:
		return "", fmt.Errorf("unsupported EPay webhook status: %q", raw)
	}
}

func shortEPayID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func BuildEPayNotifyURL(providerID string) string {
	return fmt.Sprintf("/api/v1/payments/webhook/epay/%s", providerID)
}
