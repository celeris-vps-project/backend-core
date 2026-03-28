package infra

import (
	"backend-core/internal/payment/domain"
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// MockPaymentProvider is a fake gateway that returns "pending" and fires the
// webhook callback asynchronously after a short delay, simulating a real
// redirect-based payment flow (e.g. Stripe Checkout, Alipay).
type MockPaymentProvider struct {
	seq      atomic.Int64
	callback func(payload *domain.WebhookPayload) // simulated webhook receiver
}

// NewMockPaymentProvider creates a mock provider.
// onWebhook is called asynchronously (after a 2-second delay) after every
// charge to simulate the gateway's callback. Pass nil if you don't need it.
func NewMockPaymentProvider(onWebhook func(payload *domain.WebhookPayload)) *MockPaymentProvider {
	return &MockPaymentProvider{callback: onWebhook}
}

// CreateCharge returns a "pending" status and schedules the webhook callback
// asynchronously to simulate a real payment gateway flow.
func (m *MockPaymentProvider) CreateCharge(_ context.Context, orderID string, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	chargeID := fmt.Sprintf("mock_charge_%d", m.seq.Add(1))

	log.Printf("[MockPaymentProvider] charge created (pending): id=%s order=%s %s %d", chargeID, orderID, currency, amountMinor)

	result := &domain.ChargeResult{
		ChargeID:   chargeID,
		Status:     domain.ChargeStatusPending,
		PaymentURL: fmt.Sprintf("/orders/%s/checkout", orderID), // frontend checkout page
	}

	// Simulate the gateway calling our webhook endpoint asynchronously.
	if m.callback != nil {
		go func() {
			time.Sleep(2 * time.Second) // simulate real-world latency
			log.Printf("[MockPaymentProvider] firing async webhook for charge=%s order=%s", chargeID, orderID)
			m.callback(&domain.WebhookPayload{
				ChargeID:  chargeID,
				OrderID:   orderID,
				Status:    domain.ChargeStatusSuccess,
				RawBody:   []byte(fmt.Sprintf(`{"charge_id":"%s","order_id":"%s","status":"success"}`, chargeID, orderID)),
				Signature: "mock_signature_valid",
			})
		}()
	}

	return result, nil
}

// SetCallback updates the webhook callback function.
// This allows wiring the callback after the handler is created (breaking the
// circular dependency between mock provider and payment handler).
func (m *MockPaymentProvider) SetCallback(cb func(payload *domain.WebhookPayload)) {
	m.callback = cb
}

// VerifyWebhook always returns a valid payload — no real signature check.
func (m *MockPaymentProvider) VerifyWebhook(rawBody []byte, signature string) (*domain.WebhookPayload, error) {
	// In the mock we trust everything.
	return &domain.WebhookPayload{
		ChargeID:  "mock_charge_from_webhook",
		OrderID:   "extracted_from_body",
		Status:    domain.ChargeStatusSuccess,
		RawBody:   rawBody,
		Signature: signature,
	}, nil
}
