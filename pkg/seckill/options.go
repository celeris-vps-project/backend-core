package seckill

import (
	"context"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Closure type aliases — the heart of the "generics + closures" pattern
// ─────────────────────────────────────────────────────────────────────────────

// ExecuteFunc is the core business logic callback.
// It receives the request and must return a response or error.
// This is where the caller puts their actual domain logic (e.g. create order,
// process payment, reserve VM, etc.).
type ExecuteFunc[Req any, Res any] func(ctx context.Context, req Req) (Res, error)

// KeyFunc extracts a dedup/idempotency key from the request.
// Typical implementation: "userID:productID" or "userID:activityID".
// If nil, dedup is skipped entirely.
type KeyFunc[Req any] func(req Req) string

// HookSuccess is called after a successful execute.
// Use for metrics, logging, event publishing, etc.
type HookSuccess[Req any, Res any] func(ctx context.Context, req Req, res Res)

// HookReject is called when a request is rejected at any pipeline stage.
// `reason` is a human-readable string (e.g. "sold_out", "duplicate", "gate_full").
type HookReject[Req any] func(ctx context.Context, req Req, reason string)

// LogFunc is an optional structured logger hook. If nil, log.Printf is used.
type LogFunc func(format string, args ...any)

// ─────────────────────────────────────────────────────────────────────────────
// Option — functional options for Engine construction
// ─────────────────────────────────────────────────────────────────────────────

// Option configures an Engine. Use the With* functions to create Options.
type Option[Req any, Res any] func(cfg *config[Req, Res])

// config is the internal configuration struct populated by Options.
type config[Req any, Res any] struct {
	stockTotal  int64
	gateCap     int
	dedupTTL    time.Duration
	executeFn   ExecuteFunc[Req, Res]
	keyFn       KeyFunc[Req]
	onSuccess   HookSuccess[Req, Res]
	onReject    HookReject[Req]
	logFn       LogFunc
	autoRollback bool // rollback stock on execute error (default: true)

	// Driver overrides — when set, these take priority over local defaults.
	// This enables multi-node cluster deployments (e.g. Redis-backed drivers).
	stockDriver StockDriver
	dedupDriver DedupDriver
	gateDriver  GateDriver
}

// WithStock sets the total inventory for this seckill round.
// If not set (or <= 0), the stock layer is disabled and all requests pass
// through to the execute function.
func WithStock[Req any, Res any](total int64) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.stockTotal = total
	}
}

// WithGate sets the maximum number of concurrent in-flight requests.
// If not set (or <= 0), the concurrency gate is disabled.
func WithGate[Req any, Res any](concurrency int) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.gateCap = concurrency
	}
}

// WithDedup enables idempotency checking with the given TTL.
// Requires WithKeyFunc to also be set; otherwise dedup is a no-op.
func WithDedup[Req any, Res any](ttl time.Duration) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.dedupTTL = ttl
	}
}

// WithExecute sets the core business logic closure.
// This is the only required option — the engine panics on construction
// without it.
func WithExecute[Req any, Res any](fn ExecuteFunc[Req, Res]) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.executeFn = fn
	}
}

// WithKeyFunc sets the function that extracts a dedup key from each request.
// If nil, dedup is disabled even if WithDedup was called.
func WithKeyFunc[Req any, Res any](fn KeyFunc[Req]) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.keyFn = fn
	}
}

// WithOnSuccess registers a callback invoked after each successful execute.
func WithOnSuccess[Req any, Res any](fn HookSuccess[Req, Res]) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.onSuccess = fn
	}
}

// WithOnReject registers a callback invoked when a request is rejected.
func WithOnReject[Req any, Res any](fn HookReject[Req]) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.onReject = fn
	}
}

// WithLogger sets a custom log function. Defaults to log.Printf.
func WithLogger[Req any, Res any](fn LogFunc) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.logFn = fn
	}
}

// WithAutoRollback controls whether stock is automatically rolled back
// when the execute function returns an error. Default: true.
func WithAutoRollback[Req any, Res any](enabled bool) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.autoRollback = enabled
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Driver options — for multi-node cluster deployments
// ─────────────────────────────────────────────────────────────────────────────

// WithStockDriver injects a custom StockDriver (e.g. Redis-backed).
// When set, this takes priority over WithStock (the local atomic counter).
//
// Example (Redis):
//
//	seckill.WithStockDriver[Req, Res](&RedisStock{rdb: rdb, key: "seckill:stock:123"})
func WithStockDriver[Req any, Res any](driver StockDriver) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.stockDriver = driver
	}
}

// WithDedupDriver injects a custom DedupDriver (e.g. Redis SETNX + TTL).
// When set, this takes priority over WithDedup (the local sync.Map store).
// Note: WithKeyFunc is still required for dedup to work.
//
// Example (Redis):
//
//	seckill.WithDedupDriver[Req, Res](&RedisDedup{rdb: rdb, ttl: 5*time.Minute})
func WithDedupDriver[Req any, Res any](driver DedupDriver) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.dedupDriver = driver
	}
}

// WithGateDriver injects a custom GateDriver (e.g. Redis distributed semaphore).
// When set, this takes priority over WithGate (the local channel semaphore).
//
// In most deployments, Gate should remain per-node (use WithGate instead).
// Only use WithGateDriver if you need cluster-wide concurrency control.
func WithGateDriver[Req any, Res any](driver GateDriver) Option[Req, Res] {
	return func(cfg *config[Req, Res]) {
		cfg.gateDriver = driver
	}
}
