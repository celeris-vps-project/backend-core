package infra

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/pkg/circuitbreaker"
	"context"
	"log"
)

// CatalogAdapterWithCB wraps a CatalogAdapter with circuit breaker protection.
type CatalogAdapterWithCB struct {
	inner *CatalogAdapter
	cb    *circuitbreaker.CircuitBreaker
}

func NewCatalogAdapterWithCB(inner *CatalogAdapter, cb *circuitbreaker.CircuitBreaker) *CatalogAdapterWithCB {
	log.Printf("[circuit-breaker] catalog adapter wrapped with breaker %q", cb.Name())
	return &CatalogAdapterWithCB{inner: inner, cb: cb}
}

func (a *CatalogAdapterWithCB) PurchaseProduct(ctx context.Context, productID, customerID, orderID, hostname, os string) (paymentApp.PurchasedProduct, error) {
	return circuitbreaker.Execute(a.cb, func() (paymentApp.PurchasedProduct, error) {
		return a.inner.PurchaseProduct(ctx, productID, customerID, orderID, hostname, os)
	})
}

func (a *CatalogAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}

var _ paymentApp.ProductPurchaser = (*CatalogAdapterWithCB)(nil)
