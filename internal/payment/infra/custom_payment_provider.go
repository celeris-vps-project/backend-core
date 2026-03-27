package infra

import (
	"backend-core/internal/payment/domain"
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// CustomPaymentProvider implements domain.PaymentProvider for generic / custom
// third-party payment gateways that communicate via standard webhook callbacks.
//
// The admin configures the provider with merchant credentials and an API
// endpoint. The system auto-generates a notify_url (webhook callback URL)
// that the admin registers in the third-party gateway's dashboard.
//
// Webhook flow:
//
//	Third-party gateway → POST /api/v1/payments/webhook/custom/{providerId}
//	  → VerifyWebhook (signature check with merchant_key)
//	  → HandlePaymentConfirmed → activate order → provision
//
// In MockMode, charges are auto-confirmed after a delay (same as the crypto
// provider) for development and testing.
type CustomPaymentProvider struct {
	providerID string
	config     CustomProviderConfig
	charges    sync.Map // chargeID → *customChargeRecord
	seq        atomic.Int64
	callback   func(payload *domain.WebhookPayload) // wired to payment handler
}

// CustomProviderConfig holds the parsed configuration for a custom provider.
type CustomProviderConfig struct {
	APIURL           string        // upstream payment gateway API URL
	MerchantID       string        // merchant identifier
	MerchantKey      string        // secret key for signing / verification
	SignType         string        // "md5" (default) or "hmac-sha256"
	NotifyURL        string        // auto-generated webhook callback URL
	ReturnURL        string        // browser redirect after success
	CancelURL        string        // browser redirect on cancel
	MockMode         bool          // auto-confirm for testing
	MockConfirmDelay time.Duration // delay before auto-confirm (default 3s)
}

// customChargeRecord tracks a pending charge.
type customChargeRecord struct {
	ChargeID string
	OrderID  string
	Amount   int64
	Currency string
	Status   string
}

// NewCustomPaymentProvider creates a CustomPaymentProvider from a
// PaymentProviderConfig (loaded from the database).
func NewCustomPaymentProvider(
	cfg *domain.PaymentProviderConfig,
	onWebhook func(payload *domain.WebhookPayload),
) *CustomPaymentProvider {
	parsed := parseCustomConfig(cfg)
	return &CustomPaymentProvider{
		providerID: cfg.ID,
		config:     parsed,
		callback:   onWebhook,
	}
}

// parseCustomConfig extracts typed fields from the generic config map.
func parseCustomConfig(cfg *domain.PaymentProviderConfig) CustomProviderConfig {
	c := CustomProviderConfig{
		SignType:         "md5",
		MockConfirmDelay: 3 * time.Second,
	}
	if cfg.Config == nil {
		return c
	}
	if v, ok := cfg.Config["api_url"].(string); ok {
		c.APIURL = v
	}
	if v, ok := cfg.Config["merchant_id"].(string); ok {
		c.MerchantID = v
	}
	if v, ok := cfg.Config["merchant_key"].(string); ok {
		c.MerchantKey = v
	}
	if v, ok := cfg.Config["sign_type"].(string); ok && v != "" {
		c.SignType = v
	}
	if v, ok := cfg.Config["notify_url"].(string); ok {
		c.NotifyURL = v
	}
	if v, ok := cfg.Config["return_url"].(string); ok {
		c.ReturnURL = v
	}
	if v, ok := cfg.Config["cancel_url"].(string); ok {
		c.CancelURL = v
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
func (p *CustomPaymentProvider) SetCallback(cb func(payload *domain.WebhookPayload)) {
	p.callback = cb
}

// ── PaymentProvider interface ──────────────────────────────────────────

// CreateCharge creates a pending payment charge.
//
// For custom providers with an api_url configured, this performs a server-side
// HTTP POST to the third-party gateway's API to create an order, then returns
// the gateway's payment page URL for the frontend to redirect the user to.
//
// Flow:
//  1. Build a signed JSON request body
//  2. POST to api_url (server-to-server, silent)
//  3. Parse the gateway response: { charge_id, pay_url }
//  4. Construct the full payment page URL (gateway base + pay_url)
//  5. Return ChargeResult with PaymentURL for frontend redirect
//
// In mock mode, the charge auto-confirms after a delay for testing.
func (p *CustomPaymentProvider) CreateCharge(orderID string, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	chargeID := fmt.Sprintf("custom_%s_%d", p.providerID[:8], p.seq.Add(1))

	rec := &customChargeRecord{
		ChargeID: chargeID,
		OrderID:  orderID,
		Amount:   amountMinor,
		Currency: currency,
		Status:   domain.ChargeStatusPending,
	}
	p.charges.Store(chargeID, rec)

	log.Printf("[CustomPaymentProvider:%s] charge created: id=%s order=%s %s %d",
		p.providerID[:8], chargeID, orderID, currency, amountMinor)

	var paymentURL string

	// If api_url is configured and not in mock mode, call the gateway API
	if p.config.APIURL != "" && !p.config.MockMode {
		gatewayResp, err := p.callGatewayAPI(chargeID, orderID, currency, amountMinor)
		if err != nil {
			return nil, fmt.Errorf("gateway API call failed: %w", err)
		}
		// Use gateway's charge_id if provided, otherwise keep ours
		if gatewayResp.chargeID != "" {
			chargeID = gatewayResp.chargeID
			rec.ChargeID = chargeID
		}
		paymentURL = gatewayResp.payURL

		log.Printf("[CustomPaymentProvider:%s] gateway order created: charge=%s pay_url=%s",
			p.providerID[:8], chargeID, paymentURL)
	} else {
		// Mock mode or no api_url → local checkout page
		paymentURL = fmt.Sprintf("/orders/%s/checkout", orderID)
	}

	result := &domain.ChargeResult{
		ChargeID:   chargeID,
		Status:     domain.ChargeStatusPending,
		PaymentURL: paymentURL,
	}

	// Mock mode: auto-confirm after delay
	if p.config.MockMode && p.callback != nil {
		go func() {
			time.Sleep(p.config.MockConfirmDelay)
			log.Printf("[CustomPaymentProvider:%s] MOCK: auto-confirming charge=%s order=%s",
				p.providerID[:8], chargeID, orderID)
			rec.Status = domain.ChargeStatusSuccess
			p.callback(&domain.WebhookPayload{
				ChargeID: chargeID,
				OrderID:  orderID,
				Status:   domain.ChargeStatusSuccess,
				RawBody: []byte(fmt.Sprintf(
					`{"charge_id":"%s","order_id":"%s","status":"success"}`,
					chargeID, orderID,
				)),
				Signature: "mock_custom_signature",
			})
		}()
	}

	return result, nil
}

// gatewayResponse holds the parsed response from the third-party gateway.
type gatewayResponse struct {
	chargeID string
	payURL   string
}

// callGatewayAPI performs a server-side HTTP POST to the third-party gateway
// to create a payment order. The gateway returns a charge_id and a pay_url
// that the user should be redirected to.
//
// Request (POST api_url):
//
//	{
//	  "order_id":    "...",
//	  "amount":      12345,
//	  "currency":    "USD",
//	  "merchant_id": "...",
//	  "notify_url":  "...",
//	  "return_url":  "...",
//	  "sign":        "..."
//	}
//
// Expected response:
//
//	{
//	  "code":      "SUCCESS",
//	  "charge_id": "fake_xxx",
//	  "pay_url":   "/pay/fake_xxx"
//	}
func (p *CustomPaymentProvider) callGatewayAPI(chargeID, orderID, currency string, amountMinor int64) (*gatewayResponse, error) {
	// Build the request body
	returnURL := strings.ReplaceAll(p.config.ReturnURL, "{order_id}", orderID)

	reqBody := map[string]interface{}{
		"order_id":    orderID,
		"amount":      amountMinor,
		"currency":    currency,
		"merchant_id": p.config.MerchantID,
	}
	if p.config.NotifyURL != "" {
		reqBody["notify_url"] = p.config.NotifyURL
	}
	if returnURL != "" {
		reqBody["return_url"] = returnURL
	}

	// Compute signature
	sign := p.computeSign(reqBody)
	reqBody["sign"] = sign

	// Marshal to JSON
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("[CustomPaymentProvider:%s] POST %s order=%s amount=%d",
		p.providerID[:8], p.config.APIURL, orderID, amountMinor)

	// HTTP POST to the gateway
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(p.config.APIURL, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("HTTP POST failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	log.Printf("[CustomPaymentProvider:%s] gateway response: status=%d body=%s",
		p.providerID[:8], resp.StatusCode, string(respBody))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var respData struct {
		Code     string `json:"code"`
		ChargeID string `json:"charge_id"`
		PayURL   string `json:"pay_url"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &respData); err != nil {
		return nil, fmt.Errorf("failed to parse gateway response: %w", err)
	}

	if respData.Code != "SUCCESS" {
		return nil, fmt.Errorf("gateway error: code=%s error=%s", respData.Code, respData.Error)
	}

	// Build the full payment URL by combining the gateway base URL with the pay_url
	fullPayURL := p.buildFullPayURL(respData.PayURL)

	return &gatewayResponse{
		chargeID: respData.ChargeID,
		payURL:   fullPayURL,
	}, nil
}

// buildFullPayURL constructs the full payment page URL from the gateway's
// relative pay_url and the api_url base.
//
// Example:
//
//	api_url  = "http://localhost:9090/api/v1/gateway/create"
//	pay_url  = "/pay/fake_abc123"
//	result   = "http://localhost:9090/pay/fake_abc123"
func (p *CustomPaymentProvider) buildFullPayURL(payURL string) string {
	// If pay_url is already a full URL, return as-is
	if strings.HasPrefix(payURL, "http://") || strings.HasPrefix(payURL, "https://") {
		return payURL
	}

	// Extract base URL (scheme + host) from api_url
	parsed, err := url.Parse(p.config.APIURL)
	if err != nil {
		// Fallback: return relative URL as-is
		return payURL
	}

	baseURL := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	return baseURL + payURL
}

// VerifyWebhook validates the authenticity of an incoming webhook from the
// third-party payment gateway using the configured merchant_key and sign_type.
//
// Expected body format (JSON):
//
//	{
//	  "charge_id": "...",
//	  "order_id": "...",
//	  "status": "success" | "failed",
//	  "sign": "<signature>"
//	}
//
// The signature is computed over all non-"sign" fields sorted alphabetically,
// concatenated as key=value&key=value, then signed with the merchant_key.
func (p *CustomPaymentProvider) VerifyWebhook(rawBody []byte, signature string) (*domain.WebhookPayload, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, fmt.Errorf("invalid webhook body: %w", err)
	}

	// Extract the sign field from body (takes precedence over header)
	signFromBody := ""
	if s, ok := body["sign"].(string); ok {
		signFromBody = s
	}
	// Use header signature as fallback
	actualSign := signFromBody
	if actualSign == "" {
		actualSign = signature
	}

	// Build the string-to-sign: sort all keys except "sign", concat as key=value
	if p.config.MerchantKey != "" && actualSign != "" {
		expectedSign := p.computeSign(body)
		if !strings.EqualFold(expectedSign, actualSign) {
			return nil, fmt.Errorf("webhook signature mismatch: expected=%s got=%s", expectedSign, actualSign)
		}
		log.Printf("[CustomPaymentProvider:%s] webhook signature verified", p.providerID[:8])
	} else if p.config.MerchantKey != "" {
		log.Printf("[CustomPaymentProvider:%s] WARNING: webhook received without signature, skipping verification", p.providerID[:8])
	}

	// Extract standard fields
	chargeID, _ := body["charge_id"].(string)
	orderID, _ := body["order_id"].(string)
	status, _ := body["status"].(string)

	if orderID == "" {
		return nil, fmt.Errorf("webhook body missing required field: order_id")
	}
	if status == "" {
		status = domain.ChargeStatusSuccess // default to success if not specified
	}

	// Update internal charge record if exists
	if rec, ok := p.charges.Load(chargeID); ok {
		r := rec.(*customChargeRecord)
		r.Status = status
	}

	return &domain.WebhookPayload{
		ChargeID:  chargeID,
		OrderID:   orderID,
		Status:    status,
		RawBody:   rawBody,
		Signature: actualSign,
	}, nil
}

// computeSign builds the expected signature for a webhook body.
// Algorithm: sort all keys except "sign" alphabetically, build "k1=v1&k2=v2&...&key=merchant_key",
// then hash with the configured sign_type.
func (p *CustomPaymentProvider) computeSign(body map[string]interface{}) string {
	keys := make([]string, 0, len(body))
	for k := range body {
		if k == "sign" || k == "sign_type" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		v := fmt.Sprintf("%v", body[k])
		if v == "" {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	stringToSign := strings.Join(pairs, "&")

	switch strings.ToLower(p.config.SignType) {
	case "hmac-sha256", "hmac_sha256":
		mac := hmac.New(sha256.New, []byte(p.config.MerchantKey))
		mac.Write([]byte(stringToSign))
		return hex.EncodeToString(mac.Sum(nil))
	default: // md5
		// MD5(stringToSign + "&key=" + merchantKey)
		raw := stringToSign + "&key=" + p.config.MerchantKey
		hash := md5.Sum([]byte(raw))
		return strings.ToUpper(hex.EncodeToString(hash[:]))
	}
}

// ── Static helpers ─────────────────────────────────────────────────────

// BuildCustomNotifyURL generates the standard webhook callback URL for a
// custom provider. Called after provider creation to auto-fill the notify_url.
func BuildCustomNotifyURL(providerID string) string {
	return fmt.Sprintf("/api/v1/payments/webhook/custom/%s", providerID)
}
