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
	global *TokenBucket
	perIP  *KeyLimiter
}

type KeyLimiter struct {
	rate     float64
	burst    float64
	buckets  map[string]*keyEntry
	gcTicker *time.Ticker
	stopGC   chan struct{}
	mu       sync.Mutex
}

// ipEntry wraps a per-IP bucket with a last-seen timestamp for GC.
type keyEntry struct {
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
	rl := &KeyLimiter{
		rate:    ipQPS,
		burst:   ipQPS, // burst == rate → no burst allowance beyond steady state
		buckets: make(map[string]*keyEntry),
		stopGC:  make(chan struct{}),
	}
	var globalTokenBucket *TokenBucket
	// Global bucket: capacity == QPS so a full second of burst is tolerated.
	if globalQPS > 0 {
		globalTokenBucket = NewTokenBucket(globalQPS, globalQPS)
	}

	// Start background GC for per-IP buckets every 60 seconds.
	rl.gcTicker = time.NewTicker(60 * time.Second)
	go rl.gc()

	return &RateLimiter{
		global: globalTokenBucket,
		perIP:  rl,
	}
}

func NewKeyLimiter(rate float64, burst float64) *KeyLimiter {
	rl := &KeyLimiter{
		rate:    rate,
		burst:   burst, // burst == rate → no burst allowance beyond steady state
		buckets: make(map[string]*keyEntry),
		stopGC:  make(chan struct{}),
	}
	// Start background GC for per-IP buckets every 60 seconds.
	rl.gcTicker = time.NewTicker(60 * time.Second)
	go rl.gc()
	return rl
}

// NewRateLimiterWithIPBurst with per-minute limit
func NewRateLimiterWithIPBurst(globalQPS, ipQPS, ipBurst float64) *RateLimiter {
	rl := &KeyLimiter{
		rate:    ipQPS,
		burst:   ipBurst,
		buckets: make(map[string]*keyEntry),
		stopGC:  make(chan struct{}),
	}

	var globalTokenBucket *TokenBucket
	// Global bucket: capacity == QPS so a full second of burst is tolerated.
	if globalQPS > 0 {
		globalTokenBucket = NewTokenBucket(globalQPS, globalQPS)
	}

	rl.gcTicker = time.NewTicker(60 * time.Second)
	go rl.gc()
	return &RateLimiter{
		global: globalTokenBucket,
		perIP:  rl,
	}
}

// Allow checks both the global and per-IP buckets.
// Returns true if the request should be permitted.
func (rl *RateLimiter) Allow(ip string) bool {
	// 1. Global check — fast path, no map lookup needed.
	if rl.global != nil && !rl.global.Allow() {
		return false
	}

	// 2. Per-IP check.
	if rl.perIP.rate <= 0 {
		return true // per-IP limiting disabled
	}

	bucket := rl.perIP.getOrCreate(ip)
	return bucket.Allow()
}

func (kl *KeyLimiter) Allow(key string) bool {
	if kl.rate <= 0 {
		return true
	}
	return kl.getOrCreate(key).Allow()
}

// getOrCreate returns the token bucket for the given IP, creating one if
// it does not yet exist.
func (kl *KeyLimiter) getOrCreate(ip string) *TokenBucket {
	kl.mu.Lock()
	defer kl.mu.Unlock()

	entry, ok := kl.buckets[ip]
	if !ok {
		entry = &keyEntry{
			bucket:   NewTokenBucket(kl.rate, kl.burst),
			lastSeen: time.Now(),
		}
		kl.buckets[ip] = entry
	} else {
		entry.lastSeen = time.Now()
	}
	return entry.bucket
}

// gc evicts per-IP buckets that have been idle for more than 5 minutes.
func (kl *KeyLimiter) gc() {
	const maxIdle = 5 * time.Minute
	for {
		select {
		case <-kl.gcTicker.C:
			kl.mu.Lock()
			now := time.Now()
			for ip, entry := range kl.buckets {
				if now.Sub(entry.lastSeen) > maxIdle {
					delete(kl.buckets, ip)
				}
			}
			kl.mu.Unlock()
		case <-kl.stopGC:
			kl.gcTicker.Stop()
			return
		}
	}
}

// Stop shuts down the background GC goroutine. Call this on graceful shutdown.
func (kl *KeyLimiter) Stop() {
	close(kl.stopGC)
}
