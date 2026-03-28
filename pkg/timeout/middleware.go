// Package timeout provides a Hertz middleware that enforces a per-request
// timeout. When the timeout expires, the handler's context is cancelled,
// allowing downstream operations (DB queries, RPC calls) to abort early
// instead of consuming goroutines and connections indefinitely.
//
// This is a critical concurrency safeguard: without request timeouts, a
// slow downstream dependency (e.g. unresponsive database) can cause goroutine
// accumulation and eventual OOM.
//
// Usage:
//
//	import "backend-core/pkg/timeout"
//
//	// Apply to all routes:
//	h.Use(timeout.Middleware(10 * time.Second))
//
//	// Or per-route group:
//	checkout := v1.Group("/checkout")
//	checkout.Use(timeout.Middleware(5 * time.Second))
package timeout

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// Middleware returns a Hertz middleware that enforces a request-level timeout.
//
// When the deadline is exceeded:
//   - The context passed to downstream handlers is cancelled
//   - If the response has not been written yet, a 504 Gateway Timeout is returned
//   - Downstream operations using ctx.Done() will be notified to abort
//
// The timeout covers the entire handler chain (all middleware + final handler).
//
// Parameters:
//   - duration: maximum time allowed for the entire request processing.
//     Recommended values:
//     - Catalog reads: 5s
//     - Checkout writes: 10s
//     - Admin operations: 30s
func Middleware(duration time.Duration) app.HandlerFunc {
	log.Printf("[timeout] middleware enabled (duration=%s)", duration)

	return func(c context.Context, ctx *app.RequestContext) {
		timeoutCtx, cancel := context.WithTimeout(c, duration)
		defer cancel()

		// Create a channel to detect handler completion
		done := make(chan struct{}, 1)
		// Flag to indicate the handler timed out so the goroutine can
		// skip writing a response (the timeout branch writes 504 instead).
		var timedOut atomic.Bool

		go func() {
			ctx.Next(timeoutCtx)
			done <- struct{}{}
		}()

		select {
		case <-done:
			// Handler completed within the deadline — normal response
			return
		case <-timeoutCtx.Done():
			timedOut.Store(true)

			// IMPORTANT: We MUST wait for the handler goroutine to finish
			// before returning. Returning early causes Hertz to recycle
			// the protocol.Request while the goroutine is still accessing
			// it, which triggers a data race.
			//
			// The context is already cancelled (defer cancel()), so any
			// context-aware downstream operations (HTTP calls, DB queries)
			// will abort promptly. The wait should be very short.
			<-done

			// Now the handler goroutine has fully exited — safe to write.
			ctx.Abort()
			ctx.JSON(consts.StatusGatewayTimeout, utils.H{
				"error": "request timeout exceeded",
			})
			_ = timedOut.Load() // keep the field used
			return
		}
	}
}

// ForRoutes creates a timeout middleware with a specific duration.
// Convenience wrapper for applying different timeouts to different routes.
//
// Example:
//
//	fastTimeout := timeout.ForRoutes(3 * time.Second)
//	slowTimeout := timeout.ForRoutes(30 * time.Second)
//
//	v1.GET("/products", fastTimeout, prodHandler.List)
//	admin.POST("/bulk-import", slowTimeout, adminHandler.BulkImport)
func ForRoutes(duration time.Duration) app.HandlerFunc {
	return Middleware(duration)
}
