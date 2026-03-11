// Package seckill provides a generic, high-performance flash-sale (秒杀) engine
// built on Go generics + closures. Instead of requiring callers to implement
// interfaces, all business logic is injected via function literals at
// construction time, yielding zero-boilerplate integration for any domain.
//
// Core pipeline:
//
//	Gate (concurrency semaphore)
//	  → Dedup (idempotency check)
//	    → Stock (atomic inventory)
//	      → Execute (caller-supplied closure)
//	        → Hooks (onSuccess / onReject)
package seckill

import (
	"errors"
	"sync/atomic"
)

// ─────────────────────────────────────────────────────────────────────────────
// Sentinel errors — each maps to a distinct HTTP status in the middleware layer
// ─────────────────────────────────────────────────────────────────────────────

var (
	// ErrSoldOut is returned when the stock counter reaches zero.
	// Middleware maps this to HTTP 410 Gone.
	ErrSoldOut = errors.New("seckill: sold out")

	// ErrDuplicate is returned when the same dedup key is seen twice
	// within the TTL window (e.g. same user buying the same item).
	// Middleware maps this to HTTP 409 Conflict.
	ErrDuplicate = errors.New("seckill: duplicate request")

	// ErrGateFull is returned when the concurrency gate (semaphore) is
	// at capacity and cannot accept more in-flight requests.
	// Middleware maps this to HTTP 503 Service Unavailable.
	ErrGateFull = errors.New("seckill: concurrency limit reached")

	// ErrNoExecutor is returned when Engine.Execute is called but no
	// execute function was provided via WithExecute.
	ErrNoExecutor = errors.New("seckill: no execute function configured")
)

// ─────────────────────────────────────────────────────────────────────────────
// Stats — runtime snapshot for monitoring / admin endpoints
// ─────────────────────────────────────────────────────────────────────────────

// Stats is a point-in-time snapshot of the engine's internal counters.
// Expose this via an admin endpoint for observability.
type Stats struct {
	// Stock
	StockTotal     int64 `json:"stock_total"`
	StockRemaining int64 `json:"stock_remaining"`
	StockSold      int64 `json:"stock_sold"`

	// Gate
	GateCapacity int   `json:"gate_capacity"` // 0 = unlimited
	GateInFlight int64 `json:"gate_in_flight"`

	// Dedup
	DedupBlocked int64 `json:"dedup_blocked"` // total requests rejected by dedup

	// Pipeline totals
	TotalRequests  int64 `json:"total_requests"`  // entered Execute()
	TotalSuccess   int64 `json:"total_success"`   // execute fn returned nil error
	TotalRejected  int64 `json:"total_rejected"`  // rejected at any pipeline stage
	TotalExecError int64 `json:"total_exec_error"` // execute fn returned non-nil error
}

// counters holds the atomic counters that feed Stats snapshots.
type counters struct {
	totalRequests  int64
	totalSuccess   int64
	totalRejected  int64
	totalExecError int64
	dedupBlocked   int64
}

func (c *counters) snapshot() (totalReq, totalOK, totalReject, totalExecErr, dedupBlock int64) {
	return atomic.LoadInt64(&c.totalRequests),
		atomic.LoadInt64(&c.totalSuccess),
		atomic.LoadInt64(&c.totalRejected),
		atomic.LoadInt64(&c.totalExecError),
		atomic.LoadInt64(&c.dedupBlocked)
}
