package seckill

import (
	"sync/atomic"
)

// ─────────────────────────────────────────────────────────────────────────────
// Stock — lock-free atomic inventory counter
// ─────────────────────────────────────────────────────────────────────────────

// Stock is a lock-free inventory counter backed by atomic int64 operations.
// It guarantees that no more than `total` successful acquisitions can occur,
// even under extreme concurrency — the classic flash-sale "prevent overselling"
// requirement.
//
// Algorithm: TryAcquire uses an atomic CAS loop to decrement `remaining`.
// If `remaining` is already 0, the call returns false immediately.
// This is O(1) with no mutex and zero allocations.
type Stock struct {
	total     int64 // immutable after construction (original capacity)
	remaining int64 // current available stock (decremented atomically)
}

// NewStock creates a stock counter with the given total inventory.
// Panics if total <= 0 (a seckill with no stock makes no sense).
func NewStock(total int64) *Stock {
	if total <= 0 {
		panic("seckill.NewStock: total must be > 0")
	}
	return &Stock{
		total:     total,
		remaining: total,
	}
}

// TryAcquire atomically attempts to decrement stock by one.
// Returns true if a unit was successfully reserved, false if sold out.
//
// This is the hot-path function — no locks, no allocations, pure CAS.
func (s *Stock) TryAcquire() bool {
	for {
		cur := atomic.LoadInt64(&s.remaining)
		if cur <= 0 {
			return false
		}
		if atomic.CompareAndSwapInt64(&s.remaining, cur, cur-1) {
			return true
		}
		// CAS failed → another goroutine won the race; retry
	}
}

// Rollback atomically returns one unit back to stock.
// Call this when the execute function fails after stock was already acquired,
// to avoid permanently losing inventory.
func (s *Stock) Rollback() {
	atomic.AddInt64(&s.remaining, 1)
}

// Remaining returns the current available stock (eventually consistent).
func (s *Stock) Remaining() int64 {
	return atomic.LoadInt64(&s.remaining)
}

// Sold returns the number of units that have been acquired so far.
func (s *Stock) Sold() int64 {
	return s.total - atomic.LoadInt64(&s.remaining)
}

// Total returns the original capacity.
func (s *Stock) Total() int64 {
	return s.total
}

// Reset sets the stock to a new total. Useful for admin "restock" operations
// or starting a new seckill round.
func (s *Stock) Reset(total int64) {
	atomic.StoreInt64(&s.total, total)
	atomic.StoreInt64(&s.remaining, total)
}
