// Package adaptive provides a generic QPS-based adaptive dispatch mechanism.
//
// The core idea: monitor real-time request throughput using a sliding-window
// counter, and automatically route requests to either a synchronous or
// asynchronous processor based on load. This was originally built for
// flash-sale scenarios but is designed to be business-agnostic so any
// domain (ordering, checkout, etc.) can benefit from the same pattern.
//
// Architecture:
//
//	                   ┌───────────────┐
//	  HTTP Request ──▶ │ QPSMonitor    │──▶ Record()
//	                   │ .Record()     │
//	                   └───────┬───────┘
//	                           │
//	                   ┌───────▼───────┐
//	                   │ QPS < thresh? │
//	                   └───┬───────┬───┘
//	                   YES │       │ NO
//	              ┌────────▼──┐ ┌──▼─────────┐
//	              │   Sync    │ │   Async    │
//	              │ Processor │ │ Processor  │
//	              │ (200 OK)  │ │ (202 Acc.) │
//	              └───────────┘ └────────────┘
package adaptive

import (
	"sync"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// SlidingWindowQPSMonitor — real-time QPS measurement
// ─────────────────────────────────────────────────────────────────────────────

// SlidingWindowQPSMonitor tracks request throughput using a fixed-size ring
// buffer of 1-second time buckets. Each bucket stores an atomic counter of
// requests received during that second.
//
// Algorithm:
//  1. The ring buffer has `windowSize` buckets (default: 10 = 10-second window).
//  2. On each Record() call, the current second's bucket is atomically incremented.
//  3. CurrentQPS() sums all non-expired buckets and divides by the window size.
//
// This approach provides a smooth, accurate QPS measurement with O(1) Record
// and O(windowSize) CurrentQPS, and zero allocations in the hot path.
type SlidingWindowQPSMonitor struct {
	buckets    []int64 // atomic counters, one per second
	timestamps []int64 // unix timestamp for each bucket
	windowSize int     // number of seconds in the window
	mu         sync.Mutex
}

// NewSlidingWindowQPSMonitor creates a QPS monitor with the given window size.
// windowSize is the number of seconds over which QPS is averaged (default: 10).
func NewSlidingWindowQPSMonitor(windowSize int) *SlidingWindowQPSMonitor {
	if windowSize <= 0 {
		windowSize = 10
	}
	return &SlidingWindowQPSMonitor{
		buckets:    make([]int64, windowSize),
		timestamps: make([]int64, windowSize),
		windowSize: windowSize,
	}
}

// Record registers one request. This is called on every incoming HTTP request.
// Must be lock-free in the hot path.
func (m *SlidingWindowQPSMonitor) Record() {
	now := time.Now().Unix()
	idx := int(now) % m.windowSize

	// Check if this bucket belongs to the current second
	oldTS := atomic.LoadInt64(&m.timestamps[idx])
	if oldTS == now {
		// Same second — just increment
		atomic.AddInt64(&m.buckets[idx], 1)
		return
	}

	// New second — reset the bucket (use mutex to avoid double-reset)
	m.mu.Lock()
	if atomic.LoadInt64(&m.timestamps[idx]) != now {
		atomic.StoreInt64(&m.buckets[idx], 1)
		atomic.StoreInt64(&m.timestamps[idx], now)
	} else {
		atomic.AddInt64(&m.buckets[idx], 1)
	}
	m.mu.Unlock()
}

// CurrentQPS returns the average queries-per-second over the sliding window.
// Only buckets within the window are counted; expired buckets are excluded.
func (m *SlidingWindowQPSMonitor) CurrentQPS() float64 {
	now := time.Now().Unix()
	var total int64
	var activeBuckets int

	for i := 0; i < m.windowSize; i++ {
		ts := atomic.LoadInt64(&m.timestamps[i])
		if now-ts < int64(m.windowSize) {
			total += atomic.LoadInt64(&m.buckets[i])
			activeBuckets++
		}
	}

	if activeBuckets == 0 {
		return 0
	}
	return float64(total) / float64(m.windowSize)
}

// CurrentQPSInstant returns the request count for just the current second.
// This gives a more responsive (but noisier) QPS reading.
func (m *SlidingWindowQPSMonitor) CurrentQPSInstant() int64 {
	now := time.Now().Unix()
	idx := int(now) % m.windowSize

	ts := atomic.LoadInt64(&m.timestamps[idx])
	if ts == now {
		return atomic.LoadInt64(&m.buckets[idx])
	}
	return 0
}

// QPSStats is a snapshot of the monitor's state for debugging/metrics.
type QPSStats struct {
	WindowSize    int     `json:"window_size"`
	AverageQPS    float64 `json:"average_qps"`
	InstantQPS    int64   `json:"instant_qps"`
	TotalRequests int64   `json:"total_requests_in_window"`
}

// Stats returns a snapshot of the monitor's current state.
func (m *SlidingWindowQPSMonitor) Stats() QPSStats {
	now := time.Now().Unix()
	var total int64
	for i := 0; i < m.windowSize; i++ {
		ts := atomic.LoadInt64(&m.timestamps[i])
		if now-ts < int64(m.windowSize) {
			total += atomic.LoadInt64(&m.buckets[i])
		}
	}
	return QPSStats{
		WindowSize:    m.windowSize,
		AverageQPS:    m.CurrentQPS(),
		InstantQPS:    m.CurrentQPSInstant(),
		TotalRequests: total,
	}
}
