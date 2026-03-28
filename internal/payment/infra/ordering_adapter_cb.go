package infra

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/pkg/circuitbreaker"
	"log"
)

// OrderingAdapterWithCB wraps an OrderingAdapter with circuit breaker
// protection. When the ordering service is experiencing consecutive failures,
// the circuit breaker trips open and subsequent calls fail fast with a clear
// error instead of waiting for a timeout.
//
// Degradation strategy: FAST-FAIL
//   - ActivateOrder: returns error immediately — payment flow cannot proceed
//     without order activation, so the caller must retry later.
//   - GetOrderForPayment: returns error immediately — order data is required
//     for downstream steps.
//
// The PostPaymentOrchestrator already handles these errors gracefully and
// logs them for operator awareness.
type OrderingAdapterWithCB struct {
	inner *OrderingAdapter
	cb    *circuitbreaker.CircuitBreaker
}

// NewOrderingAdapterWithCB wraps an OrderingAdapter with circuit breaker protection.
func NewOrderingAdapterWithCB(inner *OrderingAdapter, cb *circuitbreaker.CircuitBreaker) *OrderingAdapterWithCB {
	log.Printf("[circuit-breaker] ordering adapter wrapped with breaker %q", cb.Name())
	return &OrderingAdapterWithCB{inner: inner, cb: cb}
}

// ActivateOrder delegates to the inner adapter with circuit breaker protection.
// On circuit open: returns a fast-fail error.
func (a *OrderingAdapterWithCB) ActivateOrder(orderID string) error {
	return circuitbreaker.ExecuteNoResult(a.cb, func() error {
		return a.inner.ActivateOrder(orderID)
	})
}

// LinkInvoiceToOrder delegates to the inner adapter with circuit breaker protection.
// On circuit open: returns a fast-fail error.
func (a *OrderingAdapterWithCB) LinkInvoiceToOrder(orderID, invoiceID string) error {
	return circuitbreaker.ExecuteNoResult(a.cb, func() error {
		return a.inner.LinkInvoiceToOrder(orderID, invoiceID)
	})
}

// CancelOrder delegates to the inner adapter with circuit breaker protection.
// On circuit open: returns a fast-fail error.
func (a *OrderingAdapterWithCB) CancelOrder(orderID, reason string) error {
	return circuitbreaker.ExecuteNoResult(a.cb, func() error {
		return a.inner.CancelOrder(orderID, reason)
	})
}

// GetOrderForPayment delegates to the inner adapter with circuit breaker protection.
// On circuit open: returns a fast-fail error.
func (a *OrderingAdapterWithCB) GetOrderForPayment(orderID string) (paymentApp.PayableOrder, error) {
	return circuitbreaker.Execute(a.cb, func() (paymentApp.PayableOrder, error) {
		return a.inner.GetOrderForPayment(orderID)
	})
}

// Stats returns the circuit breaker's current statistics.
func (a *OrderingAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}

// Compile-time interface check
var _ paymentApp.OrderActivator = (*OrderingAdapterWithCB)(nil)
