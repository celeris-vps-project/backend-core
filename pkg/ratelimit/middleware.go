package ratelimit

import (
	"context"
	"log"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Middleware returns a Hertz middleware that enforces two-layer token-bucket
// rate limiting on every incoming request:
//
//  1. Global limit — total single-node QPS cannot exceed `globalQPS`.
//     If the global bucket is exhausted, the request is immediately rejected
//     with HTTP 429 Too Many Requests regardless of which IP it came from.
//
//  2. Per-IP limit — each unique client IP is limited to `ipQPS` requests
//     per second (default 10). If the per-IP bucket is exhausted, only that
//     client is throttled; other clients are unaffected.
//
// Both checks use the lazy-refill token bucket algorithm (see TokenBucket),
// which is O(1) per request with no background goroutines for refill.
//
// The middleware extracts the client IP from (in order):
//   - X-Forwarded-For header (first IP, for reverse-proxy setups)
//   - X-Real-Ip header
//   - Connection remote address (fallback)
func Middleware(limiter *RateLimiter) app.HandlerFunc {
	log.Printf("[ratelimit] middleware enabled")

	return func(c context.Context, ctx *app.RequestContext) {
		ip := clientIP(ctx)

		if !limiter.Allow(ip) {
			ctx.JSON(consts.StatusTooManyRequests, utils.H{
				"error": "rate limit exceeded, please try again later",
			})
			ctx.Abort()
			return
		}

		ctx.Next(c)
	}
}

// ForRoutes creates a Hertz handler middleware with specific rate limit
// settings. Use this to apply different limits to different route groups
// or individual endpoints.
//
// Example:
//
//	criticalRL := ratelimit.ForRoutes(2000, 30)
//	v1.GET("/products", criticalRL, prodHandler.List)
//
// Each call creates an independent RateLimiter instance with its own global
// bucket and per-IP bucket pool, so different route groups do not interfere
// with each other's quotas.
func ForRoutes(globalQPS, ipQPS float64) app.HandlerFunc {
	limiter := NewRateLimiter(globalQPS, ipQPS)
	return Middleware(limiter)
}

// clientIP extracts the real client IP from the request, respecting common
// reverse-proxy headers.
func clientIP(ctx *app.RequestContext) string {
	// 1. X-Forwarded-For — may contain "client, proxy1, proxy2"
	if xff := ctx.Request.Header.Get("X-Forwarded-For"); xff != "" {
		if ip, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(xff)
	}

	// 2. X-Real-Ip
	if xri := ctx.Request.Header.Get("X-Real-Ip"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// 3. Fallback to TCP remote address (strip port)
	addr := ctx.RemoteAddr().String()
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}
