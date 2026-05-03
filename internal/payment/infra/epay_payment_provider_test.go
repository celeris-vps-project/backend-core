package infra

import (
	"backend-core/internal/payment/domain"
	"errors"
	"net/url"
	"strings"
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

func TestEPayCreateChargeV1_BuildsSignedSubmitURL(t *testing.T) {
	provider := NewEPayPaymentProvider(&domain.PaymentProviderConfig{
		ID: "provider-123456",
		Config: map[string]interface{}{
			"api_url":      "https://pay.example.com/",
			"pid":          "merch_1",
			"merchant_key": "secret",
			"pay_type":     "alipay",
			"notify_url":   "https://app.example.com/api/v1/payments/webhook/epay/provider-123456",
			"return_url":   "https://app.example.com/api/v1/payments/return/epay/provider-123456",
			"product_name": "VPS Service",
		},
	}, nil)

	result, err := provider.CreateCharge(nil, "order_1", "USD", 7)
	if err != nil {
		t.Fatalf("CreateCharge returned error: %v", err)
	}
	if result.Status != domain.ChargeStatusPending {
		t.Fatalf("expected pending, got %s", result.Status)
	}
	if !strings.HasPrefix(result.PaymentURL, "https://pay.example.com/submit.php?") {
		t.Fatalf("expected submit GET url, got %s", result.PaymentURL)
	}
	parsed, err := url.Parse(result.PaymentURL)
	if err != nil {
		t.Fatalf("parse payment url: %v", err)
	}
	values := parsed.Query()
	if values.Get("out_trade_no") != "order_1" || values.Get("type") != "alipay" || values.Get("pid") != "merch_1" {
		t.Fatalf("unexpected submit params: %s", values.Encode())
	}
	if values.Get("sign") == "" || values.Get("sign_type") != "MD5" {
		t.Fatalf("expected signed submit params: %s", values.Encode())
	}
	if expected := provider.computeSignV1(formValuesToMap(values)); expected != values.Get("sign") {
		t.Fatalf("unexpected sign: got %s want %s", values.Get("sign"), expected)
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

func TestParseEPayOrderQueryResult_Success(t *testing.T) {
	result, err := parseEPayOrderQueryResult([]byte(`{
		"code": 1,
		"msg": "ok",
		"trade_no": "epay_1",
		"out_trade_no": "order_1",
		"pid": "merch_1",
		"money": "0.07",
		"status": 1
	}`), "")
	if err != nil {
		t.Fatalf("parseEPayOrderQueryResult returned error: %v", err)
	}
	if result.OrderID != "order_1" {
		t.Fatalf("expected order_1, got %s", result.OrderID)
	}
	if result.ChargeID != "epay_1" {
		t.Fatalf("expected epay_1, got %s", result.ChargeID)
	}
	if result.Status != domain.ChargeStatusSuccess {
		t.Fatalf("expected success, got %s", result.Status)
	}
	if result.ProviderMerchantID != "merch_1" {
		t.Fatalf("expected merch_1, got %s", result.ProviderMerchantID)
	}
}

func TestParseEPayOrderQueryResult_NotFound(t *testing.T) {
	_, err := parseEPayOrderQueryResult([]byte(`{"code":0,"msg":"\u8ba2\u5355\u4e0d\u5b58\u5728"}`), "order_1")
	if !errors.Is(err, domain.ErrPaymentOrderNotFound) {
		t.Fatalf("expected ErrPaymentOrderNotFound, got %v", err)
	}
}
