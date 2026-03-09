package domain

// ChargeResult represents the outcome of a payment charge request.
type ChargeResult struct {
	ChargeID   string // unique identifier from the payment gateway
	Status     string // "success", "pending", "failed"
	PaymentURL string // redirect URL for hosted checkout (empty if direct charge)
}

const (
	ChargeStatusSuccess = "success"
	ChargeStatusPending = "pending"
	ChargeStatusFailed  = "failed"
)

// WebhookPayload is the normalised data extracted from a gateway callback.
type WebhookPayload struct {
	ChargeID  string // maps back to ChargeResult.ChargeID
	OrderID   string // our internal order id
	Status    string // "success" or "failed"
	RawBody   []byte // original body for audit / debugging
	Signature string // signature header from the gateway
}

// PaymentProvider abstracts an external payment gateway (Stripe, Alipay, WeChat …).
// Implement this interface for each real gateway; during the thesis stage a Mock
// is the only implementation.
type PaymentProvider interface {
	// CreateCharge asks the gateway to create a payment for the given order.
	CreateCharge(orderID string, currency string, amountMinor int64) (*ChargeResult, error)

	// VerifyWebhook validates the authenticity of an incoming webhook callback
	// and returns the normalised payload. Returns an error if the signature is
	// invalid or the body cannot be parsed.
	VerifyWebhook(rawBody []byte, signature string) (*WebhookPayload, error)
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
