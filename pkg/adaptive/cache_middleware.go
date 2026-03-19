package adaptive

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// cacheEntry holds a cached HTTP response.
type cacheEntry struct {
	statusCode  int
	body        []byte
	contentType string
	expireAt    time.Time
}

// CacheConfig holds configuration for the adaptive cache middleware.
type CacheConfig struct {
	// Threshold is the QPS level above which caching is activated.
	// Below this threshold, all requests pass through to the handler.
	Threshold int

	// TTL is how long cached responses remain valid once cached.
	// Recommended: 3-10 seconds for catalog data.
	TTL time.Duration

	// MonitorWindow is the sliding window size (in seconds) for QPS measurement.
	// Default: 10 seconds.
	MonitorWindow int
}

// adaptiveCache is the internal state shared across middleware instances.
type adaptiveCache struct {
	monitor   *SlidingWindowQPSMonitor
	threshold int64
	ttl       time.Duration
	cache     sync.Map // map[string]*cacheEntry
	gcOnce    sync.Once
}

// CacheMiddleware creates a Hertz middleware that adaptively caches GET
// responses based on real-time QPS.
//
// Architecture:
//
//	                 ┌──────────────────┐
//	GET /products ──▶│ QPSMonitor       │──▶ Record()
//	                 └────────┬─────────┘
//	                 ┌────────▼─────────┐
//	                 │ QPS < threshold? │
//	                 └───┬──────────┬───┘
//	                 YES │          │ NO
//	            ┌────────▼──┐  ┌────▼───────────┐
//	            │ Pass-thru │  │ Check cache     │
//	            │ to handler│  │ HIT  → respond  │
//	            │ (fresh DB)│  │ MISS → handler  │
//	            └───────────┘  │   + cache (TTL) │
//	                           └─────────────────┘
//
// When QPS is below the threshold, requests pass through directly to the
// handler (DB query) for fresh data. When QPS exceeds the threshold,
// responses are served from an in-memory cache with a short TTL, dramatically
// reducing database load during traffic spikes (e.g. flash sales, promotions).
//
// Key properties:
//   - Cache key = HTTP method + full request URI (including :id params and query string)
//   - Only GET requests are cached (POST/PUT/DELETE always pass through)
//   - Cache entries expire after TTL; expired entries are lazily cleaned via background GC
//   - Thread-safe via sync.Map (optimized for read-heavy workloads)
//
// Parameters:
//   - monitor:   a shared SlidingWindowQPSMonitor (can be shared across routes)
//   - threshold: QPS level above which caching kicks in (e.g. 200)
//   - cacheTTL:  how long cached responses stay valid (e.g. 5*time.Second)
//
// Example:
//
//	monitor := adaptive.NewSlidingWindowQPSMonitor(10)
//	cache := adaptive.CacheMiddleware(monitor, 200, 5*time.Second)
//	v1.GET("/products", cache, prodHandler.List)
func CacheMiddleware(monitor *SlidingWindowQPSMonitor, threshold int, cacheTTL time.Duration) app.HandlerFunc {
	if threshold <= 0 {
		threshold = 200
	}
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Second
	}

	ac := &adaptiveCache{
		monitor:   monitor,
		threshold: int64(threshold),
		ttl:       cacheTTL,
	}

	// Start background GC goroutine (once per cache instance)
	ac.gcOnce.Do(func() {
		go ac.gc()
	})

	log.Printf("[adaptive.cache] middleware created (threshold=%d QPS, TTL=%s)", threshold, cacheTTL)

	return func(c context.Context, ctx *app.RequestContext) {
		// 1. Always record the request for QPS tracking
		ac.monitor.Record()

		// 2. Only cache GET requests
		if string(ctx.Method()) != "GET" {
			ctx.Next(c)
			return
		}

		// 3. Check if we're in high-load mode
		currentQPS := ac.monitor.CurrentQPS()
		thresh := atomic.LoadInt64(&ac.threshold)

		if currentQPS < float64(thresh) {
			// Low load — pass through for fresh data
			ctx.Next(c)
			return
		}

		// 4. High load — check cache
		cacheKey := string(ctx.Method()) + ":" + string(ctx.Request.URI().RequestURI())

		if entry, ok := ac.cache.Load(cacheKey); ok {
			ce := entry.(*cacheEntry)
			if time.Now().Before(ce.expireAt) {
				// Cache HIT — serve directly without touching the handler
				ctx.Response.Header.Set("Content-Type", ce.contentType)
				ctx.Response.Header.Set("X-Adaptive-Cache", "HIT")
				ctx.Response.SetStatusCode(ce.statusCode)
				ctx.Response.SetBody(ce.body)
				ctx.Abort()
				return
			}
			// Expired — delete and fall through
			ac.cache.Delete(cacheKey)
		}

		// 5. Cache MISS — execute handler and capture response
		ctx.Next(c)

		// Only cache successful responses (2xx)
		statusCode := ctx.Response.StatusCode()
		if statusCode >= 200 && statusCode < 300 {
			body := make([]byte, len(ctx.Response.Body()))
			copy(body, ctx.Response.Body())

			contentType := string(ctx.Response.Header.ContentType())

			ac.cache.Store(cacheKey, &cacheEntry{
				statusCode:  statusCode,
				body:        body,
				contentType: contentType,
				expireAt:    time.Now().Add(ac.ttl),
			})

			ctx.Response.Header.Set("X-Adaptive-Cache", "MISS")
		}
	}
}

// NewCacheMiddleware creates a self-contained adaptive cache middleware with
// its own QPS monitor. This is a convenience wrapper that creates both the
// monitor and the middleware in one call.
//
// Example:
//
//	cache, monitor := adaptive.NewCacheMiddleware(adaptive.CacheConfig{
//	    Threshold:     200,
//	    TTL:           5 * time.Second,
//	    MonitorWindow: 10,
//	})
func NewCacheMiddleware(cfg CacheConfig) (app.HandlerFunc, *SlidingWindowQPSMonitor) {
	if cfg.MonitorWindow <= 0 {
		cfg.MonitorWindow = 10
	}
	monitor := NewSlidingWindowQPSMonitor(cfg.MonitorWindow)
	mw := CacheMiddleware(monitor, cfg.Threshold, cfg.TTL)
	return mw, monitor
}

// gc periodically evicts expired cache entries to prevent memory leaks.
// Runs every 30 seconds in a background goroutine.
func (ac *adaptiveCache) gc() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		evicted := 0
		ac.cache.Range(func(key, value interface{}) bool {
			ce := value.(*cacheEntry)
			if now.After(ce.expireAt) {
				ac.cache.Delete(key)
				evicted++
			}
			return true
		})
		if evicted > 0 {
			log.Printf("[adaptive.cache] GC evicted %d expired entries", evicted)
		}
	}
}

// CacheStats holds statistics about the adaptive cache for monitoring.
type CacheStats struct {
	CurrentQPS    float64 `json:"current_qps"`
	Threshold     int     `json:"threshold"`
	CachingActive bool    `json:"caching_active"`
	TTL           string  `json:"ttl"`
	CachedEntries int     `json:"cached_entries"`
}
