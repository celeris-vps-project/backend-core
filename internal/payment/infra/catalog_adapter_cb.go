package infra

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/pkg/circuitbreaker"
	"log"
)

// CatalogAdapterWithCB wraps a CatalogAdapter with circuit breaker protection.
// When the catalog service is experiencing consecutive failures, the circuit
// breaker trips open and subsequent calls fail fast.
//
// Degradation strategy: FAST-FAIL
//   - PurchaseProduct: returns error immediately — slot consumption is critical
//     and cannot be skipped. The order is already activated at this point, so
//     the operator can retry the purchase manually or via a reconciliation job.
//
// Note: In the PostPaymentOrchestrator, a PurchaseProduct failure is logged
// as a WARNING but does not roll back the order activation.
type CatalogAdapterWithCB struct {
	inner *CatalogAdapter
	cb    *circuitbreaker.CircuitBreaker
}

// NewCatalogAdapterWithCB wraps a CatalogAdapter with circuit breaker protection.
func NewCatalogAdapterWithCB(inner *CatalogAdapter, cb *circuitbreaker.CircuitBreaker) *CatalogAdapterWithCB {
	log.Printf("[circuit-breaker] catalog adapter wrapped with breaker %q", cb.Name())
	return &CatalogAdapterWithCB{inner: inner, cb: cb}
}

// PurchaseProduct delegates to the inner adapter with circuit breaker protection.
// On circuit open: returns a fast-fail error with "catalog service unavailable".
func (a *CatalogAdapterWithCB) PurchaseProduct(productID, customerID, orderID, hostname, os string) (paymentApp.PurchasedProduct, error) {
	return circuitbreaker.Execute(a.cb, func() (paymentApp.PurchasedProduct, error) {
		return a.inner.PurchaseProduct(productID, customerID, orderID, hostname, os)
	})
}

// Stats returns the circuit breaker's current statistics.
func (a *CatalogAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}

// Compile-time interface check
var _ paymentApp.ProductPurchaser = (*CatalogAdapterWithCB)(nil)
