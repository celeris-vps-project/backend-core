package infra

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/pkg/circuitbreaker"
	"log"
)

// BillingAdapterWithCB wraps a BillingAdapter with circuit breaker
// protection. When the billing service is experiencing consecutive failures,
// the circuit breaker trips open and subsequent calls fail fast.
//
// Degradation strategy: SOFT DEGRADATION
//   - CreateAndIssueInvoice: returns error — payment flow can still proceed
//     without an invoice (invoiceID will be empty). The order/payment is
//     more important than the invoice record.
//   - RecordInvoicePayment: returns error — non-fatal, logged and skipped.
//   - VoidInvoice: returns error — non-fatal, orphan invoices can be cleaned
//     up by a reconciliation job later.
type BillingAdapterWithCB struct {
	inner *BillingAdapter
	cb    *circuitbreaker.CircuitBreaker
}

// NewBillingAdapterWithCB wraps a BillingAdapter with circuit breaker protection.
func NewBillingAdapterWithCB(inner *BillingAdapter, cb *circuitbreaker.CircuitBreaker) *BillingAdapterWithCB {
	log.Printf("[circuit-breaker] billing adapter wrapped with breaker %q", cb.Name())
	return &BillingAdapterWithCB{inner: inner, cb: cb}
}

// CreateAndIssueInvoice delegates to the inner adapter with circuit breaker protection.
func (a *BillingAdapterWithCB) CreateAndIssueInvoice(
	customerID, currency, billingCycle, description string, priceAmount int64,
) (string, error) {
	return circuitbreaker.Execute(a.cb, func() (string, error) {
		return a.inner.CreateAndIssueInvoice(customerID, currency, billingCycle, description, priceAmount)
	})
}

// RecordInvoicePayment delegates to the inner adapter with circuit breaker protection.
func (a *BillingAdapterWithCB) RecordInvoicePayment(invoiceID string, amount int64, currency string) error {
	return circuitbreaker.ExecuteNoResult(a.cb, func() error {
		return a.inner.RecordInvoicePayment(invoiceID, amount, currency)
	})
}

// VoidInvoice delegates to the inner adapter with circuit breaker protection.
func (a *BillingAdapterWithCB) VoidInvoice(invoiceID, reason string) error {
	return circuitbreaker.ExecuteNoResult(a.cb, func() error {
		return a.inner.VoidInvoice(invoiceID, reason)
	})
}

// GetInvoiceStatus delegates to the inner adapter with circuit breaker protection.
func (a *BillingAdapterWithCB) GetInvoiceStatus(invoiceID string) (string, error) {
	return circuitbreaker.Execute(a.cb, func() (string, error) {
		return a.inner.GetInvoiceStatus(invoiceID)
	})
}

// Stats returns the circuit breaker's current statistics.
func (a *BillingAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}

// Compile-time interface check
var _ paymentApp.InvoiceCreator = (*BillingAdapterWithCB)(nil)
