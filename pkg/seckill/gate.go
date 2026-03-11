package seckill

import "sync/atomic"

// ─────────────────────────────────────────────────────────────────────────────
// Gate — bounded concurrency semaphore (thundering-herd protection)
// ─────────────────────────────────────────────────────────────────────────────

// Gate limits the number of requests that can be processed concurrently.
// It uses a buffered channel as a counting semaphore — O(1) non-blocking
// TryEnter / Leave with zero allocations after construction.
//
// Why a Gate on top of stock?
// Stock is the final bottleneck (inventory), but the execute function
// behind stock may be slow (DB writes, RPC calls). Without a gate, all
// concurrent requests would pile up waiting for execute, consuming
// goroutines and memory. The gate sheds excess load early, keeping the
// execute-layer goroutine count bounded.
type Gate struct {
	sem      chan struct{}
	capacity int
	inFlight int64 // atomic — for Stats() reporting
}

// NewGate creates a concurrency gate with the given capacity.
// capacity must be > 0; panics otherwise.
func NewGate(capacity int) *Gate {
	if capacity <= 0 {
		panic("seckill.NewGate: capacity must be > 0")
	}
	return &Gate{
		sem:      make(chan struct{}, capacity),
		capacity: capacity,
	}
}

// TryEnter attempts to acquire a slot without blocking.
// Returns true if the caller may proceed, false if the gate is full.
//
// The caller MUST call Leave() after processing if TryEnter returned true.
func (g *Gate) TryEnter() bool {
	select {
	case g.sem <- struct{}{}:
		atomic.AddInt64(&g.inFlight, 1)
		return true
	default:
		return false
	}
}

// Leave releases a slot back to the gate. Must be called exactly once
// for each successful TryEnter.
func (g *Gate) Leave() {
	<-g.sem
	atomic.AddInt64(&g.inFlight, -1)
}

// InFlight returns the current number of in-flight requests.
func (g *Gate) InFlight() int64 {
	return atomic.LoadInt64(&g.inFlight)
}

// Capacity returns the maximum concurrency allowed.
func (g *Gate) Capacity() int {
	return g.capacity
}
