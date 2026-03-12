// Package circuitbreaker provides a generic circuit breaker implementation
// for protecting cross-domain service calls within the Celeris platform.
//
// The circuit breaker has three states:
//
//	Closed   — normal operation; requests pass through.
//	Open     — circuit is tripped; requests are rejected immediately.
//	HalfOpen — limited probe requests are allowed to test recovery.
//
// State transitions:
//
//	Closed  → Open     : after failureThreshold consecutive failures
//	Open    → HalfOpen : after timeout duration elapses
//	HalfOpen → Closed  : after successThreshold consecutive successes
//	HalfOpen → Open    : on any failure during probing
//
// Usage:
//
//	cb := circuitbreaker.New("ordering", 5, 2, 30*time.Second)
//	result, err := circuitbreaker.Execute(cb, func() (T, error) {
//	    return adapter.SomeCall(args...)
//	})
package circuitbreaker

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// State represents the current state of a circuit breaker.
type State int

const (
	StateClosed   State = iota // normal — requests pass through
	StateOpen                  // tripped — requests rejected
	StateHalfOpen              // probing — limited requests allowed
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned when the circuit breaker is in the Open state.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// CircuitBreaker implements the circuit breaker pattern for protecting
// cross-domain service calls.
type CircuitBreaker struct {
	mu sync.Mutex

	name             string
	failureThreshold int           // consecutive failures to trip the breaker
	successThreshold int           // consecutive successes in half-open to close
	timeout          time.Duration // how long to wait before probing (open → half-open)

	state            State
	consecutiveFails int
	consecutiveOK    int
	lastFailureTime  time.Time
	totalTrips       int64 // lifetime count of times the breaker has tripped
}

// New creates a new CircuitBreaker with the given parameters.
//
// Parameters:
//   - name: identifier for logging (e.g. "ordering", "catalog")
//   - failureThreshold: consecutive failures before opening the circuit
//   - successThreshold: consecutive successes in half-open before closing
//   - timeout: duration to wait in open state before allowing probe requests
func New(name string, failureThreshold, successThreshold int, timeout time.Duration) *CircuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = 5
	}
	if successThreshold <= 0 {
		successThreshold = 2
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cb := &CircuitBreaker{
		name:             name,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
		state:            StateClosed,
	}
	log.Printf("[circuit-breaker] %s initialised (failures=%d, successes=%d, timeout=%s)",
		name, failureThreshold, successThreshold, timeout)
	return cb
}

// Name returns the circuit breaker's identifier.
func (cb *CircuitBreaker) Name() string { return cb.name }

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.currentState()
}

// Stats returns a snapshot of the circuit breaker's internal counters.
type Stats struct {
	Name             string `json:"name"`
	State            string `json:"state"`
	ConsecutiveFails int    `json:"consecutive_fails"`
	ConsecutiveOK    int    `json:"consecutive_ok"`
	TotalTrips       int64  `json:"total_trips"`
}

// Stats returns a snapshot of the circuit breaker's internal counters.
func (cb *CircuitBreaker) Stats() Stats {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return Stats{
		Name:             cb.name,
		State:            cb.currentState().String(),
		ConsecutiveFails: cb.consecutiveFails,
		ConsecutiveOK:    cb.consecutiveOK,
		TotalTrips:       cb.totalTrips,
	}
}

// Allow checks whether a request should be allowed through the circuit
// breaker. Returns true if the request can proceed, false if it should
// be rejected (circuit is open).
//
// This is the low-level check; prefer Execute() for the common case.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.currentState() != StateOpen
}

// RecordSuccess records a successful call. In half-open state, it may
// transition the breaker back to closed.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails = 0

	switch cb.currentState() {
	case StateHalfOpen:
		cb.consecutiveOK++
		if cb.consecutiveOK >= cb.successThreshold {
			cb.setState(StateClosed)
			log.Printf("[circuit-breaker] %s: CLOSED (recovered after %d successes)", cb.name, cb.consecutiveOK)
			cb.consecutiveOK = 0
		}
	case StateClosed:
		// already closed, nothing special
	}
}

// RecordFailure records a failed call. May trip the breaker open.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveOK = 0
	cb.consecutiveFails++
	cb.lastFailureTime = time.Now()

	switch cb.currentState() {
	case StateClosed:
		if cb.consecutiveFails >= cb.failureThreshold {
			cb.setState(StateOpen)
			cb.totalTrips++
			log.Printf("[circuit-breaker] %s: OPEN (tripped after %d consecutive failures, total trips: %d)",
				cb.name, cb.consecutiveFails, cb.totalTrips)
		}
	case StateHalfOpen:
		// Any failure in half-open immediately re-opens the circuit
		cb.setState(StateOpen)
		cb.totalTrips++
		log.Printf("[circuit-breaker] %s: OPEN (probe failed in half-open, total trips: %d)",
			cb.name, cb.totalTrips)
	}
}

// currentState returns the effective state, accounting for timeout-based
// transition from Open → HalfOpen. Must be called with cb.mu held.
func (cb *CircuitBreaker) currentState() State {
	if cb.state == StateOpen {
		if time.Since(cb.lastFailureTime) >= cb.timeout {
			cb.setState(StateHalfOpen)
			cb.consecutiveOK = 0
			log.Printf("[circuit-breaker] %s: HALF-OPEN (timeout elapsed, probing)", cb.name)
		}
	}
	return cb.state
}

func (cb *CircuitBreaker) setState(s State) {
	cb.state = s
}

// Execute runs fn through the circuit breaker. If the circuit is open,
// it returns ErrCircuitOpen without calling fn. On success/failure of fn,
// the breaker state is updated accordingly.
//
// This is a generic helper using Go 1.18+ type parameters.
func Execute[T any](cb *CircuitBreaker, fn func() (T, error)) (T, error) {
	if !cb.Allow() {
		var zero T
		return zero, fmt.Errorf("[%s] %w", cb.name, ErrCircuitOpen)
	}

	result, err := fn()
	if err != nil {
		cb.RecordFailure()
		return result, err
	}

	cb.RecordSuccess()
	return result, nil
}

// ExecuteNoResult runs a void function through the circuit breaker.
func ExecuteNoResult(cb *CircuitBreaker, fn func() error) error {
	if !cb.Allow() {
		return fmt.Errorf("[%s] %w", cb.name, ErrCircuitOpen)
	}

	err := fn()
	if err != nil {
		cb.RecordFailure()
		return err
	}

	cb.RecordSuccess()
	return nil
}
