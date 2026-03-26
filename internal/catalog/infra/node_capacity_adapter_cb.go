package infra

import (
	"backend-core/pkg/circuitbreaker"
	"context"
	"log"
)

// NodeCapacityAdapterWithCB wraps a NodeCapacityAdapter with circuit breaker
// protection and lenient degradation for physical capacity checks.
type NodeCapacityAdapterWithCB struct {
	inner *NodeCapacityAdapter
	cb    *circuitbreaker.CircuitBreaker
}

func NewNodeCapacityAdapterWithCB(inner *NodeCapacityAdapter, cb *circuitbreaker.CircuitBreaker) *NodeCapacityAdapterWithCB {
	log.Printf("[circuit-breaker] node-capacity adapter wrapped with breaker %q (lenient fallback)", cb.Name())
	return &NodeCapacityAdapterWithCB{inner: inner, cb: cb}
}

func (a *NodeCapacityAdapterWithCB) AvailablePhysicalSlots(ctx context.Context, regionID string) (int, error) {
	if !a.cb.Allow() {
		log.Printf("[circuit-breaker] %s: physical capacity check skipped for region %s (circuit open, returning unlimited)",
			a.cb.Name(), regionID)
		return -1, nil
	}

	slots, err := a.inner.AvailablePhysicalSlots(ctx, regionID)
	if err != nil {
		a.cb.RecordFailure()
		log.Printf("[circuit-breaker] %s: physical capacity check failed for region %s: %v (returning unlimited)",
			a.cb.Name(), regionID, err)
		return -1, nil
	}

	a.cb.RecordSuccess()
	return slots, nil
}

func (a *NodeCapacityAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}
