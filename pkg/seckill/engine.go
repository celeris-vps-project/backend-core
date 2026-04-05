package seckill

import (
	"context"
	"log"
	"sync/atomic"
)

// ─────────────────────────────────────────────────────────────────────────────
// Engine — the generic seckill pipeline
// ─────────────────────────────────────────────────────────────────────────────

// Engine[Req, Res] is a high-performance, generic flash-sale pipeline.
// All domain-specific logic is injected via closures at construction time,
// making this a true "write once, use everywhere" component.
//
// Pipeline stages (each is optional except Execute):
//
//  1. Gate     — reject if too many in-flight requests (thundering herd)
//  2. Dedup    — reject if same key was seen within TTL (double buy)
//  3. Stock    — reject if inventory exhausted (overselling)
//  4. Execute  — run the caller's business logic (the closure)
//  5. Hooks    — fire onSuccess / onReject callbacks
//
// If a stage is not configured (e.g. no WithGate), it is simply skipped.
//
// Usage:
//
//	engine := seckill.New[MyReq, MyRes](
//	    seckill.WithStock[MyReq, MyRes](1000),
//	    seckill.WithGate[MyReq, MyRes](200),
//	    seckill.WithDedup[MyReq, MyRes](5 * time.Minute),
//	    seckill.WithKeyFunc[MyReq, MyRes](func(r MyReq) string { return r.UserID + ":" + r.ItemID }),
//	    seckill.WithExecute[MyReq, MyRes](func(ctx context.Context, r MyReq) (MyRes, error) {
//	        return myService.DoBusiness(r)
//	    }),
//	)
//	res, err := engine.Execute(ctx, req)
type Engine[Req any, Res any] struct {
	// Pipeline components (nil = disabled).
	// These are driver interfaces — local (*Stock, *DedupStore, *Gate) by default,
	// or distributed (Redis, etcd, etc.) when injected via WithXxxDriver options.
	stock StockDriver
	gate  GateDriver
	dedup DedupDriver

	// Closures — injected at construction
	executeFn    ExecuteFunc[Req, Res]
	keyFn        KeyFunc[Req]
	onSuccess    HookSuccess[Req, Res]
	onReject     HookReject[Req]
	logFn        LogFunc
	autoRollback bool

	// Metrics
	cnt counters
}

// New creates a new Engine with the given options.
// At minimum, WithExecute must be provided; omitting it causes a panic.
//
// Example:
//
//	engine := seckill.New[Req, Res](
//	    seckill.WithStock[Req, Res](500),
//	    seckill.WithExecute[Req, Res](myFunc),
//	)
func New[Req any, Res any](opts ...Option[Req, Res]) *Engine[Req, Res] {
	cfg := &config[Req, Res]{
		autoRollback: true, // default: rollback stock on execute error
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.executeFn == nil {
		panic("seckill.New: WithExecute is required")
	}

	e := &Engine[Req, Res]{
		executeFn:    cfg.executeFn,
		keyFn:        cfg.keyFn,
		onSuccess:    cfg.onSuccess,
		onReject:     cfg.onReject,
		logFn:        cfg.logFn,
		autoRollback: cfg.autoRollback,
	}

	// Initialize pipeline components.
	// Driver overrides take priority over local defaults.
	if cfg.stockDriver != nil {
		e.stock = cfg.stockDriver
	} else if cfg.stockTotal > 0 {
		e.stock = NewStock(cfg.stockTotal)
	}

	if cfg.gateDriver != nil {
		e.gate = cfg.gateDriver
	} else if cfg.gateCap > 0 {
		e.gate = NewGate(cfg.gateCap)
	}

	if cfg.dedupDriver != nil {
		e.dedup = cfg.dedupDriver
	} else if cfg.dedupTTL > 0 && cfg.keyFn != nil {
		e.dedup = NewDedupStore(cfg.dedupTTL)
	}

	if e.logFn == nil {
		e.logFn = log.Printf
	}

	e.logFn("[seckill] engine created: stock=%v gate=%v dedup=%v (driver overrides: stock=%v dedup=%v gate=%v)",
		e.stock != nil, e.gate != nil, e.dedup != nil,
		cfg.stockDriver != nil, cfg.dedupDriver != nil, cfg.gateDriver != nil)

	return e
}

// Execute runs the full seckill pipeline for the given request.
//
// Pipeline: Gate → Dedup → Stock → executeFn → Hooks
//
// Returns:
//   - (Res, nil)          on success
//   - (zero, ErrGateFull) if concurrency gate is full
//   - (zero, ErrDuplicate) if dedup key was already seen
//   - (zero, ErrSoldOut)  if stock is exhausted
//   - (zero, err)         if the execute function itself failed
func (e *Engine[Req, Res]) Execute(ctx context.Context, req Req) (res Res, err error) {
	atomic.AddInt64(&e.cnt.totalRequests, 1)

	// ── Stage 1: Gate (concurrency limiter) ──────────────────────────────
	if e.gate != nil {
		if !e.gate.TryEnter() {
			atomic.AddInt64(&e.cnt.totalRejected, 1)
			e.fireReject(ctx, req, "gate_full")
			return res, ErrGateFull
		}
		defer e.gate.Leave()
	}

	// ── Stage 2: Dedup (idempotency) ────────────────────────────────────
	var dedupKey string
	if e.dedup != nil && e.keyFn != nil {
		dedupKey = e.keyFn(req)
		if dedupKey != "" && !e.dedup.MarkIfAbsent(dedupKey) {
			atomic.AddInt64(&e.cnt.totalRejected, 1)
			e.fireReject(ctx, req, "duplicate")
			return res, ErrDuplicate
		}
	}

	// ── Stage 3: Stock (inventory) ──────────────────────────────────────
	if e.stock != nil {
		if !e.stock.TryAcquire() {
			atomic.AddInt64(&e.cnt.totalRejected, 1)
			// Remove dedup mark so user can retry if stock is replenished
			if e.dedup != nil && dedupKey != "" {
				e.dedup.Remove(dedupKey)
			}
			e.fireReject(ctx, req, "sold_out")
			return res, ErrSoldOut
		}
	}

	// ── Stage 4: Execute (caller's business logic) ──────────────────────
	res, err = e.executeFn(ctx, req)
	if err != nil {
		atomic.AddInt64(&e.cnt.totalExecError, 1)

		// Rollback stock if configured
		if e.autoRollback && e.stock != nil {
			e.stock.Rollback()
		}
		// Remove dedup mark so user can retry
		if e.dedup != nil && dedupKey != "" {
			e.dedup.Remove(dedupKey)
		}

		e.fireReject(ctx, req, "exec_error: "+err.Error())
		return res, err
	}

	// ── Stage 5: Success ────────────────────────────────────────────────
	atomic.AddInt64(&e.cnt.totalSuccess, 1)
	e.fireSuccess(ctx, req, res)

	return res, nil
}

// Stats returns a point-in-time snapshot of engine metrics.
func (e *Engine[Req, Res]) Stats() Stats {
	totalReq, totalOK, totalReject, totalExecErr, dedupBlock := e.cnt.snapshot()

	s := Stats{
		TotalRequests:  totalReq,
		TotalSuccess:   totalOK,
		TotalRejected:  totalReject,
		TotalExecError: totalExecErr,
		DedupBlocked:   dedupBlock,
	}

	if e.stock != nil {
		s.StockTotal = e.stock.Total()
		s.StockRemaining = e.stock.Remaining()
		s.StockSold = e.stock.Sold()
	}

	if e.gate != nil {
		s.GateCapacity = e.gate.Capacity()
		s.GateInFlight = e.gate.InFlight()
	}

	if e.dedup != nil {
		s.DedupBlocked = e.dedup.Blocked()
	}

	return s
}

// StockDriver returns the underlying stock driver (nil if not configured).
// Useful for admin operations like Reset().
// For local mode, you can type-assert to *Stock if needed.
func (e *Engine[Req, Res]) StockDriver() StockDriver {
	return e.stock
}

// GateDriver returns the underlying gate driver (nil if not configured).
func (e *Engine[Req, Res]) GateDriver() GateDriver {
	return e.gate
}

// DedupDriver returns the underlying dedup driver (nil if not configured).
func (e *Engine[Req, Res]) DedupDriver() DedupDriver {
	return e.dedup
}

// Stop gracefully shuts down background goroutines (dedup GC).
// Call this on application shutdown.
func (e *Engine[Req, Res]) Stop() {
	if e.dedup != nil {
		e.dedup.Stop()
	}
}

// ── internal helpers ────────────────────────────────────────────────────────

func (e *Engine[Req, Res]) fireSuccess(ctx context.Context, req Req, res Res) {
	if e.onSuccess != nil {
		e.onSuccess(ctx, req, res)
	}
}

func (e *Engine[Req, Res]) fireReject(ctx context.Context, req Req, reason string) {
	e.logFn("[seckill] rejected: %s", reason)
	if e.onReject != nil {
		e.onReject(ctx, req, reason)
	}
}
