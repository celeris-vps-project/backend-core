package infra

import (
	"backend-core/internal/payment/domain"
	"net/url"
	"testing"
)

func TestEPayVerifyWebhook_FormMD5Success(t *testing.T) {
	provider := NewEPayPaymentProvider(&domain.PaymentProviderConfig{
		ID: "provider-123456",
		Config: map[string]interface{}{
			"merchant_key": "secret",
		},
	}, nil)

	values := url.Values{}
	values.Set("out_trade_no", "order_1")
	values.Set("trade_no", "epay_1")
	values.Set("status", "1")
	values.Set("money", "0.07")
	values.Set("sign", provider.computeSignV1(formValuesToMap(values)))
	values.Set("sign_type", "MD5")

	payload, err := provider.VerifyWebhook([]byte(values.Encode()), nil)
	if err != nil {
		t.Fatalf("VerifyWebhook returned error: %v", err)
	}
	if payload.OrderID != "order_1" {
		t.Fatalf("expected order id order_1, got %s", payload.OrderID)
	}
	if payload.ChargeID != "epay_1" {
		t.Fatalf("expected charge id epay_1, got %s", payload.ChargeID)
	}
	if payload.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected success status, got %s", payload.Status)
	}
}

func TestEPayVerifyWebhook_ReturnQueryMD5Success(t *testing.T) {
	provider := NewEPayPaymentProvider(&domain.PaymentProviderConfig{
		ID: "provider-123456",
		Config: map[string]interface{}{
			"merchant_key": "secret",
		},
	}, nil)

	values := url.Values{}
	values.Set("pid", "merch_1")
	values.Set("out_trade_no", "order_1")
	values.Set("trade_no", "epay_1")
	values.Set("trade_status", "TRADE_SUCCESS")
	values.Set("money", "0.07")
	values.Set("sign", provider.computeSignV1(formValuesToMap(values)))
	values.Set("sign_type", "MD5")

	payload, err := provider.VerifyWebhook([]byte(values.Encode()), nil)
	if err != nil {
		t.Fatalf("VerifyWebhook returned error: %v", err)
	}
	if payload.OrderID != "order_1" {
		t.Fatalf("expected order id order_1, got %s", payload.OrderID)
	}
	if payload.ChargeID != "epay_1" {
		t.Fatalf("expected charge id epay_1, got %s", payload.ChargeID)
	}
	if payload.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected success status, got %s", payload.Status)
	}
}

func TestEPayVerifyWebhook_FormMD5Pending(t *testing.T) {
	provider := NewEPayPaymentProvider(&domain.PaymentProviderConfig{
		ID: "provider-123456",
		Config: map[string]interface{}{
			"merchant_key": "secret",
		},
	}, nil)

	values := url.Values{}
	values.Set("out_trade_no", "order_2")
	values.Set("trade_no", "epay_2")
	values.Set("status", "0")
	values.Set("sign", provider.computeSignV1(formValuesToMap(values)))
	values.Set("sign_type", "MD5")

	payload, err := provider.VerifyWebhook([]byte(values.Encode()), nil)
	if err != nil {
		t.Fatalf("VerifyWebhook returned error: %v", err)
	}
	if payload.Status != domain.ChargeStatusPending {
		t.Fatalf("expected pending status, got %s", payload.Status)
	}
}

func TestEPayVerifyWebhook_RejectsBadSignature(t *testing.T) {
	provider := NewEPayPaymentProvider(&domain.PaymentProviderConfig{
		ID: "provider-123456",
		Config: map[string]interface{}{
			"merchant_key": "secret",
		},
	}, nil)

	values := url.Values{}
	values.Set("out_trade_no", "order_1")
	values.Set("trade_no", "epay_1")
	values.Set("status", "1")
	values.Set("sign", "bad")
	values.Set("sign_type", "MD5")

	if _, err := provider.VerifyWebhook([]byte(values.Encode()), nil); err == nil {
		t.Fatal("expected bad signature error")
	}
}
