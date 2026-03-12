package infra

import (
	"backend-core/pkg/circuitbreaker"
	"log"
)

// NodeCapacityAdapterWithCB wraps a NodeCapacityAdapter with circuit breaker
// protection and lenient degradation for physical capacity checks.
//
// Degradation strategy: LENIENT FALLBACK
//   - AvailablePhysicalSlots: when the circuit is open, returns -1 (unlimited).
//     This allows admin stock adjustments to proceed without physical capacity
//     validation. The admin will not see a physical capacity warning, but the
//     adjustment will succeed.
//
// This is safe because:
//   - Physical capacity is a soft-limit advisory, not a hard constraint
//   - The admin receives a warning when capacity is exceeded (when available)
//   - Provisioning will queue requests if physical capacity is insufficient
//
// When the provisioning service recovers, subsequent AdjustStock calls will
// resume normal physical capacity checking.
type NodeCapacityAdapterWithCB struct {
	inner *NodeCapacityAdapter
	cb    *circuitbreaker.CircuitBreaker
}

// NewNodeCapacityAdapterWithCB wraps a NodeCapacityAdapter with circuit breaker
// protection and lenient fallback.
func NewNodeCapacityAdapterWithCB(inner *NodeCapacityAdapter, cb *circuitbreaker.CircuitBreaker) *NodeCapacityAdapterWithCB {
	log.Printf("[circuit-breaker] node-capacity adapter wrapped with breaker %q (lenient fallback)", cb.Name())
	return &NodeCapacityAdapterWithCB{inner: inner, cb: cb}
}

// AvailablePhysicalSlots delegates to the inner adapter with circuit breaker.
// On circuit open: returns -1 (unlimited) — skip physical capacity validation.
func (a *NodeCapacityAdapterWithCB) AvailablePhysicalSlots(regionID string) (int, error) {
	if !a.cb.Allow() {
		log.Printf("[circuit-breaker] %s: physical capacity check skipped for region %s (circuit open, returning unlimited)",
			a.cb.Name(), regionID)
		return -1, nil // -1 = unlimited, skips the soft-limit warning
	}

	slots, err := a.inner.AvailablePhysicalSlots(regionID)
	if err != nil {
		a.cb.RecordFailure()
		log.Printf("[circuit-breaker] %s: physical capacity check failed for region %s: %v (returning unlimited)",
			a.cb.Name(), regionID, err)
		return -1, nil // lenient fallback — don't block admin operations
	}

	a.cb.RecordSuccess()
	return slots, nil
}

// Stats returns the circuit breaker's current statistics.
func (a *NodeCapacityAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}
