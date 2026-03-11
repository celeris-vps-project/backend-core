package perf

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// Middleware returns a Hertz middleware that records every request into the
// EndpointTracker. It captures method, path, latency, status code, and
// whether the request was mitigated (HTTP 202 on checkout endpoint).
func Middleware(tracker *EndpointTracker) app.HandlerFunc {
	return func(c context.Context, ctx *app.RequestContext) {
		start := time.Now()

		// Execute the next handler
		ctx.Next(c)

		// Record metrics after response
		latencyMs := float64(time.Since(start).Microseconds()) / 1000.0
		method := string(ctx.Method())
		path := string(ctx.Request.URI().Path())
		statusCode := ctx.Response.StatusCode()

		// A request is "mitigated" if it was downgraded to async processing.
		// The checkout endpoint returns 202 Accepted when in async mode.
		isMitigated := statusCode == 202

		tracker.Record(method, path, latencyMs, statusCode, isMitigated)
	}
}
