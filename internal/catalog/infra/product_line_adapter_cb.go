package infra

import (
	catalogApp "backend-core/internal/catalog/app"
	"backend-core/pkg/circuitbreaker"
	"log"
	"sync"
)

// ProductLineAdapterWithCB wraps a ProvisioningProductLineAdapter with circuit
// breaker protection and cache-based degradation.
//
// Degradation strategy: CACHE FALLBACK
//   - ListActiveResourcePools: when the circuit is open, returns the last
//     successfully cached result. If no cache exists, returns an empty list.
//   - ListActiveRegions: same cache fallback strategy.
//
// This ensures that the product browsing experience degrades gracefully when
// the provisioning service is temporarily unavailable. Users can still see
// the product lines (albeit potentially stale) instead of getting an error page.
//
// Cache is updated on every successful call and has no TTL — it's only used
// as a fallback when the circuit is open.
type ProductLineAdapterWithCB struct {
	inner *ProvisioningProductLineAdapter
	cb    *circuitbreaker.CircuitBreaker

	mu          sync.RWMutex
	cachedPools []catalogApp.ResourcePoolInfo
	cachedRegns []catalogApp.RegionInfo
}

// NewProductLineAdapterWithCB wraps a ProvisioningProductLineAdapter with
// circuit breaker protection and cache-based degradation.
func NewProductLineAdapterWithCB(inner *ProvisioningProductLineAdapter, cb *circuitbreaker.CircuitBreaker) *ProductLineAdapterWithCB {
	log.Printf("[circuit-breaker] product-line adapter wrapped with breaker %q (cache fallback)", cb.Name())
	return &ProductLineAdapterWithCB{inner: inner, cb: cb}
}

// ListActiveResourcePools delegates to the inner adapter with circuit breaker.
// On circuit open: returns cached data from the last successful call.
func (a *ProductLineAdapterWithCB) ListActiveResourcePools() ([]catalogApp.ResourcePoolInfo, error) {
	if !a.cb.Allow() {
		a.mu.RLock()
		cached := a.cachedPools
		a.mu.RUnlock()
		if cached != nil {
			log.Printf("[circuit-breaker] %s: serving %d cached resource pools (circuit open)",
				a.cb.Name(), len(cached))
			return cached, nil
		}
		log.Printf("[circuit-breaker] %s: no cached resource pools available (circuit open)", a.cb.Name())
		return []catalogApp.ResourcePoolInfo{}, nil
	}

	pools, err := a.inner.ListActiveResourcePools()
	if err != nil {
		a.cb.RecordFailure()
		// On failure, try to serve cache
		a.mu.RLock()
		cached := a.cachedPools
		a.mu.RUnlock()
		if cached != nil {
			log.Printf("[circuit-breaker] %s: serving %d cached resource pools (call failed: %v)",
				a.cb.Name(), len(cached), err)
			return cached, nil
		}
		return nil, err
	}

	a.cb.RecordSuccess()
	// Update cache
	a.mu.Lock()
	a.cachedPools = pools
	a.mu.Unlock()
	return pools, nil
}

// ListActiveRegions delegates to the inner adapter with circuit breaker.
// On circuit open: returns cached data from the last successful call.
func (a *ProductLineAdapterWithCB) ListActiveRegions() ([]catalogApp.RegionInfo, error) {
	if !a.cb.Allow() {
		a.mu.RLock()
		cached := a.cachedRegns
		a.mu.RUnlock()
		if cached != nil {
			log.Printf("[circuit-breaker] %s: serving %d cached regions (circuit open)",
				a.cb.Name(), len(cached))
			return cached, nil
		}
		log.Printf("[circuit-breaker] %s: no cached regions available (circuit open)", a.cb.Name())
		return []catalogApp.RegionInfo{}, nil
	}

	regions, err := a.inner.ListActiveRegions()
	if err != nil {
		a.cb.RecordFailure()
		// On failure, try to serve cache
		a.mu.RLock()
		cached := a.cachedRegns
		a.mu.RUnlock()
		if cached != nil {
			log.Printf("[circuit-breaker] %s: serving %d cached regions (call failed: %v)",
				a.cb.Name(), len(cached), err)
			return cached, nil
		}
		return nil, err
	}

	a.cb.RecordSuccess()
	// Update cache
	a.mu.Lock()
	a.cachedRegns = regions
	a.mu.Unlock()
	return regions, nil
}

// Stats returns the circuit breaker's current statistics.
func (a *ProductLineAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}

// Compile-time interface check
var _ catalogApp.ProductLineDataSource = (*ProductLineAdapterWithCB)(nil)
