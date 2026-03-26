package infra

import (
	catalogApp "backend-core/internal/catalog/app"
	"backend-core/pkg/circuitbreaker"
	"context"
	"log"
	"sync"
)

// ProductLineAdapterWithCB wraps a ProvisioningProductLineAdapter with circuit
// breaker protection and cache-based degradation.
type ProductLineAdapterWithCB struct {
	inner *ProvisioningProductLineAdapter
	cb    *circuitbreaker.CircuitBreaker

	mu          sync.RWMutex
	cachedPools []catalogApp.ResourcePoolInfo
	cachedRegns []catalogApp.RegionInfo
}

func NewProductLineAdapterWithCB(inner *ProvisioningProductLineAdapter, cb *circuitbreaker.CircuitBreaker) *ProductLineAdapterWithCB {
	log.Printf("[circuit-breaker] product-line adapter wrapped with breaker %q (cache fallback)", cb.Name())
	return &ProductLineAdapterWithCB{inner: inner, cb: cb}
}

func (a *ProductLineAdapterWithCB) ListActiveResourcePools(ctx context.Context) ([]catalogApp.ResourcePoolInfo, error) {
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

	pools, err := a.inner.ListActiveResourcePools(ctx)
	if err != nil {
		a.cb.RecordFailure()
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
	a.mu.Lock()
	a.cachedPools = pools
	a.mu.Unlock()
	return pools, nil
}

func (a *ProductLineAdapterWithCB) ListActiveRegions(ctx context.Context) ([]catalogApp.RegionInfo, error) {
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

	regions, err := a.inner.ListActiveRegions(ctx)
	if err != nil {
		a.cb.RecordFailure()
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
	a.mu.Lock()
	a.cachedRegns = regions
	a.mu.Unlock()
	return regions, nil
}

func (a *ProductLineAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}

var _ catalogApp.ProductLineDataSource = (*ProductLineAdapterWithCB)(nil)
