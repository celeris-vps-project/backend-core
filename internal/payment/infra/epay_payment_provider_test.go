package infra

import (
	"backend-core/internal/payment/domain"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestEPayVerifyWebhook_JSONWithDefaultHeaders(t *testing.T) {
	provider := NewEPayPaymentProvider(&domain.PaymentProviderConfig{
		ID: "provider-123456",
		Config: map[string]interface{}{
			"webhook_secret": "secret",
		},
	}, nil)

	body := []byte(`{"data":{"id":"pi_1","amount":10000,"currency":"USD","merchant_order_id":"order_1","result":{"result_code":"SUCCEEDED"}}}`)
	headers := signedWebhookHeaders("secret", "1704628800000", body, "timestamp", "signature")

	payload, err := provider.VerifyWebhook(body, headers)
	if err != nil {
		t.Fatalf("VerifyWebhook returned error: %v", err)
	}
	if payload.OrderID != "order_1" {
		t.Fatalf("expected order id order_1, got %s", payload.OrderID)
	}
	if payload.ChargeID != "pi_1" {
		t.Fatalf("expected charge id pi_1, got %s", payload.ChargeID)
	}
	if payload.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected success status, got %s", payload.Status)
	}
}

func TestEPayVerifyWebhook_FormWithCustomHeaders(t *testing.T) {
	provider := NewEPayPaymentProvider(&domain.PaymentProviderConfig{
		ID: "provider-123456",
		Config: map[string]interface{}{
			"webhook_secret":   "secret",
			"timestamp_header": "X-Kyren-Timestamp",
			"signature_header": "X-Kyren-Signature",
		},
	}, nil)

	body := []byte("merchantOrderNo=order_2&epayOrderNo=epay_2&status=7")
	headers := signedWebhookHeaders("secret", "1704628800000", body, "x-kyren-timestamp", "x-kyren-signature")

	payload, err := provider.VerifyWebhook(body, headers)
	if err != nil {
		t.Fatalf("VerifyWebhook returned error: %v", err)
	}
	if payload.OrderID != "order_2" {
		t.Fatalf("expected order id order_2, got %s", payload.OrderID)
	}
	if payload.ChargeID != "epay_2" {
		t.Fatalf("expected charge id epay_2, got %s", payload.ChargeID)
	}
	if payload.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected success status, got %s", payload.Status)
	}
}

func TestEPayVerifyWebhook_RejectsBadSignature(t *testing.T) {
	provider := NewEPayPaymentProvider(&domain.PaymentProviderConfig{
		ID: "provider-123456",
		Config: map[string]interface{}{
			"webhook_secret": "secret",
		},
	}, nil)

	body := []byte(`{"data":{"id":"pi_1","merchant_order_id":"order_1","result":{"result_code":"SUCCEEDED"}}}`)
	headers := domain.WebhookHeaders{
		"timestamp": "1704628800000",
		"signature": "sha256=bad",
	}

	if _, err := provider.VerifyWebhook(body, headers); err == nil {
		t.Fatal("expected bad signature error")
	}
}

func signedWebhookHeaders(secret, timestamp string, body []byte, timestampHeader, signatureHeader string) domain.WebhookHeaders {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)

	return domain.WebhookHeaders{
		timestampHeader: timestamp,
		signatureHeader: "sha256=" + hex.EncodeToString(mac.Sum(nil)),
	}
}
