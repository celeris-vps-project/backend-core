package domain

import (
	"context"
	"errors"
	"time"
)

// ChargeResult represents the outcome of a payment charge request.
type ChargeResult struct {
	ChargeID   string              // unique identifier from the payment gateway
	OutTradeNo string              // merchant-side payment attempt id sent to the gateway
	Status     string              // "success", "pending", "failed"
	PaymentURL string              // redirect URL for hosted checkout (empty if direct charge)
	Crypto     *CryptoChargeDetail // non-nil when payment is crypto (USDT)
}

const (
	ChargeStatusSuccess = "success"
	ChargeStatusPending = "pending"
	ChargeStatusFailed  = "failed"
)

var ErrPaymentOrderNotFound = errors.New("payment order not found")
var ErrPaymentAttemptNotFound = errors.New("payment attempt not found")

// WebhookPayload is the normalised data extracted from a gateway callback.
type WebhookPayload struct {
	ChargeID  string // maps back to ChargeResult.ChargeID
	OrderID   string // our internal order id
	Status    string // "success" or "failed"
	RawBody   []byte // original body for audit / debugging
	Signature string // signature header from the gateway
}

// WebhookHeaders contains HTTP headers from a gateway callback.
// Keys should be normalised to lower-case by the HTTP adapter.
type WebhookHeaders map[string]string

type PaymentOrderQuery struct {
	OutTradeNo string
	TradeNo    string
}

type PaymentOrderQueryResult struct {
	ChargeID           string
	OrderID            string
	Status             string
	Amount             string
	ProviderMerchantID string
	RawBody            []byte
}

func (r *PaymentOrderQueryResult) WebhookPayload() *WebhookPayload {
	if r == nil {
		return nil
	}
	return &WebhookPayload{
		ChargeID: r.ChargeID,
		OrderID:  r.OrderID,
		Status:   r.Status,
		RawBody:  r.RawBody,
	}
}

type PaymentOrderQuerier interface {
	QueryOrder(ctx context.Context, query PaymentOrderQuery) (*PaymentOrderQueryResult, error)
}

type PaymentAttempt struct {
	ID         string
	OrderID    string
	ProviderID string
	PayType    string
	TradeNo    string
	OutTradeNo string
	PayURL     string
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type PaymentAttemptRepo interface {
	Create(ctx context.Context, attempt *PaymentAttempt) error
	Update(ctx context.Context, attempt *PaymentAttempt) error
	FindLatestByOrderID(ctx context.Context, orderID string) (*PaymentAttempt, error)
	FindByOutTradeNo(ctx context.Context, outTradeNo string) (*PaymentAttempt, error)
	ListPending(ctx context.Context, limit int) ([]*PaymentAttempt, error)
}

// PaymentProvider abstracts an external payment gateway (Stripe, Alipay, WeChat …).
// Implement this interface for each real gateway; during the thesis stage a Mock
// is the only implementation.
type PaymentProvider interface {
	// CreateCharge asks the gateway to create a payment for the given order.
	// The context should be used for cancellation and deadline propagation
	// to downstream HTTP calls (e.g. EPay gateway API).
	CreateCharge(ctx context.Context, orderID string, currency string, amountMinor int64) (*ChargeResult, error)

	// VerifyWebhook validates the authenticity of an incoming webhook callback
	// and returns the normalised payload. Returns an error if the signature is
	// invalid or the body cannot be parsed.
	VerifyWebhook(rawBody []byte, headers WebhookHeaders) (*WebhookPayload, error)
}

// CryptoPaymentProvider extends PaymentProvider with crypto-specific operations.
// Implementations handle USDT payments across multiple blockchain networks.
type CryptoPaymentProvider interface {
	PaymentProvider

	// CreateCryptoCharge creates a payment charge on a specific blockchain network.
	CreateCryptoCharge(ctx context.Context, orderID string, amountMinor int64, network CryptoNetwork) (*ChargeResult, error)

	// GetNetworks returns all supported blockchain networks and their info.
	GetNetworks() []NetworkInfo

	// GetChargeDetail returns the crypto-specific details for a charge.
	// Returns nil if the charge is not found.
	GetChargeDetail(chargeID string) *CryptoChargeDetail
}

// CheckoutProcessor is the application-level entry-point for the payment
// bounded context. It orchestrates: create charge → (await webhook) → notify
// other domains. Keeping it as an interface so the implementation can live in
// Go today and migrate to a Java micro-service later without touching callers.
type CheckoutProcessor interface {
	// Process initiates a payment for the given order and amount.
	// Returns the ChargeResult so the caller can redirect the user if needed.
	Process(orderID string, currency string, amountMinor int64) (*ChargeResult, error)
}
