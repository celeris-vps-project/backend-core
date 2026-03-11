package ratelimit

import (
	"sync"
	"time"
)

// TokenBucket implements the classic token-bucket rate limiting algorithm.
// Tokens are added at a constant rate (refillRate tokens per second) up to a
// maximum of `capacity`. Each Allow() call consumes one token; if no tokens
// are available the call returns false (request should be rejected).
//
// The implementation uses lazy refill — tokens are computed on-demand based
// on elapsed time, avoiding the need for a background goroutine.
type TokenBucket struct {
	mu         sync.Mutex
	capacity   float64   // max burst size (== max tokens)
	tokens     float64   // current available tokens
	refillRate float64   // tokens added per second
	lastRefill time.Time // last time tokens were recalculated
}

// NewTokenBucket creates a token bucket that refills at `rate` tokens/second
// with a maximum burst size of `capacity`.
// The bucket starts full (tokens == capacity).
func NewTokenBucket(rate float64, capacity float64) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		refillRate: rate,
		lastRefill: time.Now(),
	}
}

// Allow attempts to consume one token. Returns true if a token was available
// (request is permitted), false otherwise (request should be rejected).
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
	tb.lastRefill = now

	if tb.tokens < 1 {
		return false
	}
	tb.tokens--
	return true
}
