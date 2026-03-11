package ratelimit

import (
	"sync"
	"time"
)

// RateLimiter combines a global token bucket (whole-server QPS cap) with
// per-IP token buckets (per-client fairness). Both layers must pass for a
// request to be allowed.
//
// Usage:
//
//	rl := ratelimit.NewRateLimiter(1000, 10)  // global 1000 QPS, per-IP 10 QPS
//	if !rl.Allow(clientIP) { /* reject */ }
type RateLimiter struct {
	global    *TokenBucket
	ipRate    float64 // per-IP tokens/sec
	ipBurst   float64 // per-IP max burst
	mu        sync.Mutex
	buckets   map[string]*ipEntry
	gcTicker  *time.Ticker
	stopGC    chan struct{}
}

// ipEntry wraps a per-IP bucket with a last-seen timestamp for GC.
type ipEntry struct {
	bucket   *TokenBucket
	lastSeen time.Time
}

// NewRateLimiter creates a two-layer rate limiter.
//   - globalQPS: maximum requests per second across ALL clients (0 = unlimited).
//   - ipQPS:     maximum requests per second per unique IP (0 = unlimited).
//
// A background goroutine periodically evicts stale per-IP buckets (idle > 5 min)
// to prevent memory leaks from long-tail IPs.
func NewRateLimiter(globalQPS float64, ipQPS float64) *RateLimiter {
	rl := &RateLimiter{
		ipRate:  ipQPS,
		ipBurst: ipQPS, // burst == rate → no burst allowance beyond steady state
		buckets: make(map[string]*ipEntry),
		stopGC:  make(chan struct{}),
	}

	// Global bucket: capacity == QPS so a full second of burst is tolerated.
	if globalQPS > 0 {
		rl.global = NewTokenBucket(globalQPS, globalQPS)
	}

	// Start background GC for per-IP buckets every 60 seconds.
	rl.gcTicker = time.NewTicker(60 * time.Second)
	go rl.gc()

	return rl
}

// Allow checks both the global and per-IP buckets.
// Returns true if the request should be permitted.
func (rl *RateLimiter) Allow(ip string) bool {
	// 1. Global check — fast path, no map lookup needed.
	if rl.global != nil && !rl.global.Allow() {
		return false
	}

	// 2. Per-IP check.
	if rl.ipRate <= 0 {
		return true // per-IP limiting disabled
	}

	bucket := rl.getOrCreate(ip)
	return bucket.Allow()
}

// getOrCreate returns the token bucket for the given IP, creating one if
// it does not yet exist.
func (rl *RateLimiter) getOrCreate(ip string) *TokenBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.buckets[ip]
	if !ok {
		entry = &ipEntry{
			bucket:   NewTokenBucket(rl.ipRate, rl.ipBurst),
			lastSeen: time.Now(),
		}
		rl.buckets[ip] = entry
	} else {
		entry.lastSeen = time.Now()
	}
	return entry.bucket
}

// gc evicts per-IP buckets that have been idle for more than 5 minutes.
func (rl *RateLimiter) gc() {
	const maxIdle = 5 * time.Minute
	for {
		select {
		case <-rl.gcTicker.C:
			rl.mu.Lock()
			now := time.Now()
			for ip, entry := range rl.buckets {
				if now.Sub(entry.lastSeen) > maxIdle {
					delete(rl.buckets, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopGC:
			rl.gcTicker.Stop()
			return
		}
	}
}

// Stop shuts down the background GC goroutine. Call this on graceful shutdown.
func (rl *RateLimiter) Stop() {
	close(rl.stopGC)
}
