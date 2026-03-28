package infra

import (
	"backend-core/internal/payment/domain"
	"context"
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// EPayPaymentProvider implements domain.PaymentProvider for EPay (易支付)
// third-party payment gateways, supporting both V1 (MD5) and V2 (RSA) APIs.
//
// EPay is a popular payment aggregation platform in China that supports
// Alipay, WeChat Pay, QQ Pay and other payment channels through a unified API.
//
// V1 API:
//   - Submit (redirect): {api_url}/submit.php
//   - API (server-side): {api_url}/mapi.php
//   - Signing: MD5  →  sign = md5(sorted_params + KEY), lowercase
//   - Notify: GET with query parameters, return "success"
//
// V2 API:
//   - Submit (redirect): {api_url}/api/pay/submit
//   - Create (server-side): {api_url}/api/pay/create
//   - Signing: SHA256WithRSA (merchant private key)
//   - Verification: SHA256WithRSA (platform public key)
//   - Notify: GET with query parameters, return "success"
//
// Mock mode auto-confirms payments after a delay for development/testing.

var (
	// httpClient is shared across all EPay providers. Individual requests
	// use context-based deadlines, so the client timeout is set generously
	// as a safety net only.
	httpClient = &http.Client{Timeout: 30 * time.Second}
)

const (
	epayMaxRetries     = 2             // up to 2 retries (3 attempts total)
	epayPerAttemptTime = 8 * time.Second // per-attempt timeout
	epayBaseBackoff    = 1 * time.Second // initial backoff between retries
)

type EPayPaymentProvider struct {
	providerID string
	config     EPayProviderConfig
	charges    sync.Map // chargeID → *epayChargeRecord
	seq        atomic.Int64
	callback   func(payload *domain.WebhookPayload) // wired to payment handler
}

// EPayProviderConfig holds the parsed configuration for an EPay provider.
type EPayProviderConfig struct {
	APIURL                string          // EPay gateway base URL (e.g. https://pay.myzfw.com)
	PID                   string          // Merchant ID
	APIVersion            string          // "v1" or "v2" (default "v2")
	MerchantKey           string          // V1: MD5 secret key
	MerchantPrivateKey    *rsa.PrivateKey // V2: RSA private key for signing
	PlatformPublicKey     *rsa.PublicKey  // V2: RSA public key for verification
	MerchantPrivateKeyPEM string          // raw PEM (for error messages)
	PlatformPublicKeyPEM  string          // raw PEM (for error messages)
	PayType               string          // Default payment type (alipay, wxpay, etc.)
	NotifyURL             string          // Async callback URL
	ReturnURL             string          // Browser redirect URL
	ProductName           string          // Product name template (default: "VPS Service")
	MockMode              bool            // Auto-confirm for testing
	MockConfirmDelay      time.Duration   // Delay before auto-confirm (default 3s)
}

// epayChargeRecord tracks a pending EPay charge.
type epayChargeRecord struct {
	ChargeID string
	OrderID  string
	Amount   int64
	Currency string
	Status   string
}

// NewEPayPaymentProvider creates an EPayPaymentProvider from a
// PaymentProviderConfig (loaded from the database).
func NewEPayPaymentProvider(
	cfg *domain.PaymentProviderConfig,
	onWebhook func(payload *domain.WebhookPayload),
) *EPayPaymentProvider {
	parsed := parseEPayConfig(cfg)
	return &EPayPaymentProvider{
		providerID: cfg.ID,
		config:     parsed,
		callback:   onWebhook,
	}
}

// parseEPayConfig extracts typed fields from the generic config map.
func parseEPayConfig(cfg *domain.PaymentProviderConfig) EPayProviderConfig {
	c := EPayProviderConfig{
		APIVersion:       "v2",
		ProductName:      "VPS Service",
		MockConfirmDelay: 3 * time.Second,
	}
	if cfg.Config == nil {
		return c
	}
	if v, ok := cfg.Config["api_url"].(string); ok {
		c.APIURL = strings.TrimRight(v, "/")
	}
	if v, ok := cfg.Config["pid"].(string); ok {
		c.PID = v
	}
	if v, ok := cfg.Config["api_version"].(string); ok && v != "" {
		c.APIVersion = strings.ToLower(v)
	}
	if v, ok := cfg.Config["merchant_key"].(string); ok {
		c.MerchantKey = v
	}
	if v, ok := cfg.Config["merchant_private_key"].(string); ok && v != "" {
		c.MerchantPrivateKeyPEM = v
		if key, err := parseRSAPrivateKey(v); err == nil {
			c.MerchantPrivateKey = key
		} else {
			log.Printf("[EPayProvider] WARNING: failed to parse merchant private key: %v", err)
		}
	}
	if v, ok := cfg.Config["platform_public_key"].(string); ok && v != "" {
		c.PlatformPublicKeyPEM = v
		if key, err := parseRSAPublicKey(v); err == nil {
			c.PlatformPublicKey = key
		} else {
			log.Printf("[EPayProvider] WARNING: failed to parse platform public key: %v", err)
		}
	}
	if v, ok := cfg.Config["pay_type"].(string); ok {
		c.PayType = v
	}
	if v, ok := cfg.Config["notify_url"].(string); ok {
		c.NotifyURL = v
	}
	if v, ok := cfg.Config["return_url"].(string); ok {
		c.ReturnURL = v
	}
	if v, ok := cfg.Config["product_name"].(string); ok && v != "" {
		c.ProductName = v
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

// SetCallback updates the webhook callback function.
func (p *EPayPaymentProvider) SetCallback(cb func(payload *domain.WebhookPayload)) {
	p.callback = cb
}

// ── PaymentProvider interface ──────────────────────────────────────────

// CreateCharge creates a payment order at the EPay gateway.
//
// For V1: calls {api_url}/mapi.php with MD5-signed form params, returns redirect URL.
// For V2: calls {api_url}/api/pay/create with RSA-signed form params, returns pay_info.
//
// In mock mode, the charge auto-confirms after a delay for testing.
func (p *EPayPaymentProvider) CreateCharge(ctx context.Context, orderID string, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	chargeID := fmt.Sprintf("epay_%s_%d", p.providerID[:8], p.seq.Add(1))

	rec := &epayChargeRecord{
		ChargeID: chargeID,
		OrderID:  orderID,
		Amount:   amountMinor,
		Currency: currency,
		Status:   domain.ChargeStatusPending,
	}
	p.charges.Store(chargeID, rec)

	// Convert amountMinor (cents) to EPay money format (yuan, 2 decimal places)
	money := fmt.Sprintf("%.2f", float64(amountMinor)/100.0)

	log.Printf("[EPayProvider:%s] charge creating: id=%s order=%s %s (¥%s) version=%s",
		p.providerID[:8], chargeID, orderID, currency, money, p.config.APIVersion)

	var paymentURL string
	var err error

	if p.config.MockMode {
		// Mock mode → local checkout page, auto-confirm
		paymentURL = fmt.Sprintf("/orders/%s/checkout", orderID)
	} else if p.config.APIVersion == "v1" {
		paymentURL, err = p.createChargeV1(chargeID, orderID, money)
	} else {
		paymentURL, err = p.createChargeV2(ctx, chargeID, orderID, money)
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
		p.providerID[:8], chargeID, orderID, paymentURL)

	// Mock mode: auto-confirm after delay
	if p.config.MockMode && p.callback != nil {
		go func() {
			time.Sleep(p.config.MockConfirmDelay)
			log.Printf("[EPayProvider:%s] MOCK: auto-confirming charge=%s order=%s",
				p.providerID[:8], chargeID, orderID)
			rec.Status = domain.ChargeStatusSuccess
			p.callback(&domain.WebhookPayload{
				ChargeID: chargeID,
				OrderID:  orderID,
				Status:   domain.ChargeStatusSuccess,
				RawBody: []byte(fmt.Sprintf(
					`{"charge_id":"%s","order_id":"%s","status":"success","trade_status":"TRADE_SUCCESS"}`,
					chargeID, orderID,
				)),
				Signature: "mock_epay_signature",
			})
		}()
	}

	return result, nil
}

// ── V1 Implementation ──────────────────────────────────────────────────

// createChargeV1 calls the V1 mapi.php endpoint to create a payment.
// Returns a redirect URL (submit.php with signed params) for the user.
//
// V1 uses form POST to mapi.php. For simplicity, we build the submit.php
// redirect URL directly (equivalent to form submission).
func (p *EPayPaymentProvider) createChargeV1(chargeID, orderID, money string) (string, error) {
	returnURL := strings.ReplaceAll(p.config.ReturnURL, "{order_id}", orderID)

	// Build parameters for signing
	params := map[string]string{
		"pid":          p.config.PID,
		"out_trade_no": orderID,
		"notify_url":   p.config.NotifyURL,
		"return_url":   returnURL,
		"name":         p.config.ProductName,
		"money":        money,
	}
	if p.config.PayType != "" {
		params["type"] = p.config.PayType
	}

	// Compute MD5 signature
	sign := p.computeSignV1(params)
	params["sign"] = sign
	params["sign_type"] = "MD5"

	// Build redirect URL: submit.php?pid=...&type=...&sign=...
	submitURL := p.config.APIURL + "/submit.php"
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}

	fullURL := submitURL + "?" + q.Encode()

	log.Printf("[EPayProvider:%s] V1 redirect URL built: order=%s url=%s",
		p.providerID[:8], orderID, submitURL)

	return fullURL, nil
}

// ── V2 Implementation ──────────────────────────────────────────────────

// createChargeV2 calls the V2 /api/pay/create endpoint to create a payment.
// Uses context-aware HTTP requests with retry and exponential backoff for
// transient failures (timeouts, 5xx errors).
//
// Returns the payment URL from the gateway response.
func (p *EPayPaymentProvider) createChargeV2(ctx context.Context, chargeID, orderID, money string) (string, error) {
	if p.config.MerchantPrivateKey == nil {
		return "", fmt.Errorf("V2 requires merchant_private_key (RSA PEM)")
	}

	returnURL := strings.ReplaceAll(p.config.ReturnURL, "{order_id}", orderID)
	timestamp := fmt.Sprintf("%d", time.Now().Unix())

	// Build parameters for signing
	params := map[string]string{
		"pid":          p.config.PID,
		"method":       "jump", // use jump to always get a redirect URL
		"out_trade_no": orderID,
		"notify_url":   p.config.NotifyURL,
		"return_url":   returnURL,
		"name":         p.config.ProductName,
		"money":        money,
		"timestamp":    timestamp,
		"clientip":     "127.0.0.1",
		"device":       "pc",
	}
	if p.config.PayType != "" {
		params["type"] = p.config.PayType
	}

	// Compute RSA signature
	sign, err := p.computeSignV2(params)
	if err != nil {
		return "", fmt.Errorf("V2 RSA sign failed: %w", err)
	}
	params["sign"] = sign
	params["sign_type"] = "RSA"

	// POST to /api/pay/create as form-encoded
	createURL := p.config.APIURL + "/api/pay/create"

	formData := url.Values{}
	for k, v := range params {
		formData.Set(k, v)
	}
	encodedForm := formData.Encode()

	log.Printf("[EPayProvider:%s] V2 POST %s order=%s money=%s",
		p.providerID[:8], createURL, orderID, money)

	// ── Retry loop with exponential backoff ────────────────────────────
	var lastErr error
	for attempt := 0; attempt <= epayMaxRetries; attempt++ {
		if attempt > 0 {
			// Check if parent context is already cancelled before retrying
			if ctx.Err() != nil {
				return "", fmt.Errorf("context cancelled before retry %d: %w", attempt, ctx.Err())
			}
			backoff := time.Duration(float64(epayBaseBackoff) * math.Pow(2, float64(attempt-1)))
			log.Printf("[EPayProvider:%s] V2 retry %d/%d after %v (order=%s): %v",
				p.providerID[:8], attempt, epayMaxRetries, backoff, orderID, lastErr)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return "", fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
			}
		}

		paymentURL, err := p.doV2Post(ctx, createURL, encodedForm, orderID)
		if err == nil {
			return paymentURL, nil
		}
		lastErr = err

		// Only retry on transient errors (timeouts, network errors, 5xx)
		if !isTransientError(err) {
			return "", lastErr
		}
	}

	return "", fmt.Errorf("all %d attempts failed: %w", epayMaxRetries+1, lastErr)
}

// doV2Post executes a single HTTP POST to the EPay V2 create endpoint.
// Uses a per-attempt context timeout derived from the parent context.
func (p *EPayPaymentProvider) doV2Post(ctx context.Context, createURL, encodedForm, orderID string) (string, error) {
	// Create a per-attempt timeout (shorter than the overall request timeout)
	attemptCtx, cancel := context.WithTimeout(ctx, epayPerAttemptTime)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, createURL,
		strings.NewReader(encodedForm))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP POST failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	log.Printf("[EPayProvider:%s] V2 gateway response: status=%d body=%s",
		p.providerID[:8], resp.StatusCode, string(respBody))

	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("gateway returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	if resp.StatusCode != http.StatusOK {
		// 4xx errors are not transient — don't retry
		return "", &permanentError{msg: fmt.Sprintf("gateway returned HTTP %d: %s", resp.StatusCode, string(respBody))}
	}

	// Parse response JSON
	var respData struct {
		Code      int    `json:"code"`
		Msg       string `json:"msg"`
		TradeNo   string `json:"trade_no"`
		PayType   string `json:"pay_type"`
		PayInfo   string `json:"pay_info"`
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
		SignType  string `json:"sign_type"`
	}
	if err := json.Unmarshal(respBody, &respData); err != nil {
		return "", fmt.Errorf("failed to parse gateway response: %w", err)
	}

	if respData.Code != 0 {
		return "", &permanentError{msg: fmt.Sprintf("gateway error: code=%d msg=%s", respData.Code, respData.Msg)}
	}

	// Verify response signature if platform public key is configured
	if p.config.PlatformPublicKey != nil && respData.Sign != "" {
		respParams := map[string]string{
			"code":      fmt.Sprintf("%d", respData.Code),
			"trade_no":  respData.TradeNo,
			"pay_type":  respData.PayType,
			"pay_info":  respData.PayInfo,
			"timestamp": respData.Timestamp,
		}
		if err := p.verifySignV2(respParams, respData.Sign); err != nil {
			log.Printf("[EPayProvider:%s] WARNING: response signature verification failed: %v (continuing anyway)",
				p.providerID[:8], err)
		}
	}

	// Determine payment URL based on pay_type
	paymentURL := respData.PayInfo
	if respData.PayType == "jump" || respData.PayType == "html" {
		if !strings.HasPrefix(paymentURL, "http") {
			paymentURL = p.config.APIURL + paymentURL
		}
	}

	log.Printf("[EPayProvider:%s] V2 charge created: trade_no=%s pay_type=%s pay_info=%s",
		p.providerID[:8], respData.TradeNo, respData.PayType, paymentURL)

	return paymentURL, nil
}

// permanentError marks an error as non-retryable (e.g. 4xx, business logic error).
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

// isTransientError returns true for errors that are worth retrying:
// timeouts, connection resets, 5xx responses. Returns false for
// permanent errors (4xx, business logic errors).
func isTransientError(err error) bool {
	// permanentError is explicitly non-retryable
	var pe *permanentError
	if errors.As(err, &pe) {
		return false
	}
	// context.Canceled means the caller gave up — don't retry
	if errors.Is(err, context.Canceled) {
		return false
	}
	// context.DeadlineExceeded from the per-attempt timeout is transient
	// (the parent context might still have budget)
	return true
}

// ── Webhook Verification ───────────────────────────────────────────────

// VerifyWebhook validates the authenticity of an incoming EPay webhook callback.
//
// EPay sends callbacks as GET requests with query parameters:
//
//	GET /api/v1/payments/webhook/epay/{providerId}?pid=...&trade_no=...&out_trade_no=...
//	    &type=alipay&trade_status=TRADE_SUCCESS&name=...&money=1.00&sign=...&sign_type=MD5|RSA
//
// For V1: verifies MD5 signature using merchant_key
// For V2: verifies RSA signature using platform_public_key
//
// The rawBody parameter should contain the raw query string (not JSON).
// The signature parameter is ignored (sign is in the query params).
func (p *EPayPaymentProvider) VerifyWebhook(rawBody []byte, signature string) (*domain.WebhookPayload, error) {
	// Parse the query string from rawBody
	queryStr := string(rawBody)
	values, err := url.ParseQuery(queryStr)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook query string: %w", err)
	}

	// Extract the sign and sign_type
	actualSign := values.Get("sign")
	signType := values.Get("sign_type")

	// Build params map (excluding sign and sign_type)
	params := make(map[string]string)
	for k := range values {
		if k == "sign" || k == "sign_type" {
			continue
		}
		v := values.Get(k)
		if v != "" {
			params[k] = v
		}
	}

	// Verify signature based on sign_type (or api_version as fallback)
	if signType == "RSA" || (signType == "" && p.config.APIVersion == "v2") {
		// V2 RSA verification
		if p.config.PlatformPublicKey == nil {
			log.Printf("[EPayProvider:%s] WARNING: webhook RSA verification skipped (no platform_public_key)",
				p.providerID[:8])
		} else if actualSign != "" {
			if err := p.verifySignV2(params, actualSign); err != nil {
				return nil, fmt.Errorf("webhook RSA signature verification failed: %w", err)
			}
			log.Printf("[EPayProvider:%s] webhook RSA signature verified", p.providerID[:8])
		}
	} else {
		// V1 MD5 verification
		if p.config.MerchantKey != "" && actualSign != "" {
			expectedSign := p.computeSignV1(params)
			if !strings.EqualFold(expectedSign, actualSign) {
				return nil, fmt.Errorf("webhook MD5 signature mismatch: expected=%s got=%s", expectedSign, actualSign)
			}
			log.Printf("[EPayProvider:%s] webhook MD5 signature verified", p.providerID[:8])
		} else if p.config.MerchantKey != "" {
			log.Printf("[EPayProvider:%s] WARNING: webhook received without signature", p.providerID[:8])
		}
	}

	// Extract standard EPay fields
	tradeStatus := values.Get("trade_status")
	outTradeNo := values.Get("out_trade_no")
	tradeNo := values.Get("trade_no")

	if outTradeNo == "" {
		return nil, fmt.Errorf("webhook missing required field: out_trade_no")
	}

	// Map EPay trade_status to our status
	status := domain.ChargeStatusFailed
	if tradeStatus == "TRADE_SUCCESS" {
		status = domain.ChargeStatusSuccess
	}

	// Update internal charge record if exists
	p.charges.Range(func(key, value interface{}) bool {
		r := value.(*epayChargeRecord)
		if r.OrderID == outTradeNo {
			r.Status = status
			return false // stop iteration
		}
		return true
	})

	log.Printf("[EPayProvider:%s] webhook received: trade_no=%s out_trade_no=%s status=%s trade_status=%s",
		p.providerID[:8], tradeNo, outTradeNo, status, tradeStatus)

	return &domain.WebhookPayload{
		ChargeID:  tradeNo,    // EPay's trade_no
		OrderID:   outTradeNo, // our order ID
		Status:    status,
		RawBody:   rawBody,
		Signature: actualSign,
	}, nil
}

// ── V1 MD5 Signing ─────────────────────────────────────────────────────

// computeSignV1 builds the MD5 signature for V1 API.
// Algorithm:
//  1. Sort all non-empty params by key (ASCII ascending), exclude sign/sign_type
//  2. Concatenate as "key1=value1&key2=value2&..."
//  3. Append merchant key: stringToSign + KEY
//  4. MD5 hash → lowercase hex
func (p *EPayPaymentProvider) computeSignV1(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		v := params[k]
		if v == "" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	stringToSign := strings.Join(pairs, "&")

	// MD5(stringToSign + KEY) — note: no "&key=" separator in EPay V1
	raw := stringToSign + p.config.MerchantKey
	hash := md5.Sum([]byte(raw))
	return strings.ToLower(hex.EncodeToString(hash[:]))
}

// ── V2 RSA Signing ─────────────────────────────────────────────────────

// computeSignV2 builds the RSA (SHA256WithRSA) signature for V2 API.
// Algorithm:
//  1. Sort all non-empty params by key (ASCII ascending), exclude sign/sign_type
//  2. Concatenate as "key1=value1&key2=value2&..."
//  3. Sign with merchant private key using SHA256WithRSA
//  4. Base64 encode the signature
func (p *EPayPaymentProvider) computeSignV2(params map[string]string) (string, error) {
	stringToSign := p.buildStringToSign(params)

	hashed := sha256.Sum256([]byte(stringToSign))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.config.MerchantPrivateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("RSA sign failed: %w", err)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// verifySignV2 verifies the RSA (SHA256WithRSA) signature using the platform public key.
func (p *EPayPaymentProvider) verifySignV2(params map[string]string, signBase64 string) error {
	stringToSign := p.buildStringToSign(params)

	signBytes, err := base64.StdEncoding.DecodeString(signBase64)
	if err != nil {
		return fmt.Errorf("invalid base64 signature: %w", err)
	}

	hashed := sha256.Sum256([]byte(stringToSign))
	return rsa.VerifyPKCS1v15(p.config.PlatformPublicKey, crypto.SHA256, hashed[:], signBytes)
}

// buildStringToSign creates the canonical string for signing.
// Sorts params by key, excludes sign/sign_type and empty values.
func (p *EPayPaymentProvider) buildStringToSign(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		v := params[k]
		if v == "" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, "&")
}

// ── RSA Key Parsing ────────────────────────────────────────────────────

// parseRSAPrivateKey parses a PEM-encoded RSA private key.
// Accepts PKCS1 or PKCS8 format.
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		// Try as raw base64 (no PEM headers)
		derBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pemStr))
		if err != nil {
			return nil, fmt.Errorf("failed to decode PEM block or base64")
		}
		// Try PKCS8 first, then PKCS1
		if key, err := x509.ParsePKCS8PrivateKey(derBytes); err == nil {
			if rsaKey, ok := key.(*rsa.PrivateKey); ok {
				return rsaKey, nil
			}
			return nil, fmt.Errorf("PKCS8 key is not RSA")
		}
		return x509.ParsePKCS1PrivateKey(derBytes)
	}

	// PEM block found — try PKCS8 first, then PKCS1
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("PKCS8 key is not RSA")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// parseRSAPublicKey parses a PEM-encoded RSA public key.
// Accepts PKIX format.
func parseRSAPublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		// Try as raw base64 (no PEM headers)
		derBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pemStr))
		if err != nil {
			return nil, fmt.Errorf("failed to decode PEM block or base64")
		}
		pub, err := x509.ParsePKIXPublicKey(derBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse PKIX public key: %w", err)
		}
		if rsaKey, ok := pub.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("public key is not RSA")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PKIX public key: %w", err)
	}
	if rsaKey, ok := pub.(*rsa.PublicKey); ok {
		return rsaKey, nil
	}
	return nil, fmt.Errorf("public key is not RSA")
}

// ── Static helpers ─────────────────────────────────────────────────────

// BuildEPayNotifyURL generates the standard webhook callback URL for an
// EPay provider. Called after provider creation to auto-fill the notify_url.
func BuildEPayNotifyURL(providerID string) string {
	return fmt.Sprintf("/api/v1/payments/webhook/epay/%s", providerID)
}
