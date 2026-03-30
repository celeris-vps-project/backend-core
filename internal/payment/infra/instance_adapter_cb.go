package infra

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/pkg/circuitbreaker"
	"log"
)

// InstanceAdapterWithCB wraps an InstanceAdapter with circuit breaker protection
// and graceful degradation. When the instance service is experiencing failures,
// the circuit breaker trips open.
//
// Degradation strategy: SILENT DEGRADATION
//   - CreatePendingInstance: when the circuit is open, returns ("", nil) instead
//     of an error. This is safe because:
//     1. Instance creation is already designed as non-fatal in the PostPaymentOrchestrator
//     2. The provisioning bus will handle actual VM provisioning asynchronously
//     3. A reconciliation job can create missing instance records later
//
// This ensures that instance service failures never block the payment
// confirmation flow.
type InstanceAdapterWithCB struct {
	inner *InstanceAdapter
	cb    *circuitbreaker.CircuitBreaker
}

// NewInstanceAdapterWithCB wraps an InstanceAdapter with circuit breaker protection.
func NewInstanceAdapterWithCB(inner *InstanceAdapter, cb *circuitbreaker.CircuitBreaker) *InstanceAdapterWithCB {
	log.Printf("[circuit-breaker] instance adapter wrapped with breaker %q", cb.Name())
	return &InstanceAdapterWithCB{inner: inner, cb: cb}
}

// CreatePendingInstance delegates to the inner adapter with circuit breaker protection.
//
// On circuit open: returns ("", nil) — silent degradation. The instance will
// be created later via reconciliation when the service recovers.
func (a *InstanceAdapterWithCB) CreatePendingInstance(
	customerID, orderID, region, hostname, plan, os string,
	cpu, memoryMB, diskGB int,
) (string, error) {
	if !a.cb.Allow() {
		log.Printf("[circuit-breaker] %s: instance creation skipped (circuit open), order=%s will be reconciled",
			a.cb.Name(), orderID)
		return "", nil // silent degradation — non-fatal
	}

	instanceID, err := a.inner.CreatePendingInstance(customerID, orderID, region, hostname, plan, os, cpu, memoryMB, diskGB)
	if err != nil {
		a.cb.RecordFailure()
		log.Printf("[circuit-breaker] %s: instance creation failed for order=%s: %v", a.cb.Name(), orderID, err)
		// Still return nil error for graceful degradation
		return "", nil
	}

	a.cb.RecordSuccess()
	return instanceID, nil
}

func (a *InstanceAdapterWithCB) GetByOrderID(orderID string) (paymentApp.RenewalInstance, error) {
	return circuitbreaker.Execute(a.cb, func() (paymentApp.RenewalInstance, error) {
		return a.inner.GetByOrderID(orderID)
	})
}

func (a *InstanceAdapterWithCB) SuspendInstance(instanceID string) error {
	return circuitbreaker.ExecuteNoResult(a.cb, func() error {
		return a.inner.SuspendInstance(instanceID)
	})
}

func (a *InstanceAdapterWithCB) RecoverFromBillingSuspension(instanceID string) error {
	return circuitbreaker.ExecuteNoResult(a.cb, func() error {
		return a.inner.RecoverFromBillingSuspension(instanceID)
	})
}

// Stats returns the circuit breaker's current statistics.
func (a *InstanceAdapterWithCB) Stats() circuitbreaker.Stats {
	return a.cb.Stats()
}

// Compile-time interface check
var _ paymentApp.InstanceCreator = (*InstanceAdapterWithCB)(nil)
var _ paymentApp.RenewalInstanceManager = (*InstanceAdapterWithCB)(nil)
