package http

import (
	"backend-core/internal/checkout/domain"
	"backend-core/internal/checkout/infra"
	"backend-core/pkg/adaptive"
	"backend-core/pkg/authn"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	hertzApp "github.com/cloudwego/hertz/pkg/app"
)

// CheckoutHandler exposes HTTP endpoints for the unified checkout module.
//
// Endpoints:
//   - POST /checkout                    → adaptive sync/async checkout
//   - GET  /checkout/orders/:id         → poll async order status (legacy/fallback)
//   - GET  /checkout/orders/:id/stream  → SSE stream for async order status (preferred)
//   - GET  /checkout/stats              → QPS monitor stats (admin/debug)
//   - PUT  /checkout/threshold          → update QPS threshold (admin)
type CheckoutHandler struct {
	dispatcher  *adaptive.Dispatcher[domain.CheckoutRequest, *domain.CheckoutResult]
	statusStore *infra.OrderStatusStore
}

// NewCheckoutHandler creates a new handler wired to the adaptive dispatcher.
func NewCheckoutHandler(
	dispatcher *adaptive.Dispatcher[domain.CheckoutRequest, *domain.CheckoutResult],
	statusStore *infra.OrderStatusStore,
) *CheckoutHandler {
	return &CheckoutHandler{
		dispatcher:  dispatcher,
		statusStore: statusStore,
	}
}

// Checkout handles POST /checkout
//
// The adaptive dispatcher automatically routes to sync (200) or async (202)
// based on current QPS. The frontend should handle both status codes:
//   - 200: purchase completed synchronously
//   - 202: order queued, subscribe to SSE via /checkout/orders/:id/stream
func (h *CheckoutHandler) Checkout(c context.Context, ctx *hertzApp.RequestContext) {
	// Extract customer ID from JWT context (set by auth middleware)
	uid, ok := authn.UserID(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, map[string]string{
			"error": "unauthorized — login required",
		})
		return
	}

	var req domain.CheckoutRequest
	if err := ctx.BindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid request body: " + err.Error(),
		})
		return
	}

	// Override customer ID from JWT (don't trust client-provided value)
	req.CustomerID = uid.String()

	result, err := h.dispatcher.Dispatch(req)
	if err != nil {
		if isSlotError(err) {
			ctx.JSON(http.StatusConflict, map[string]string{
				"error": err.Error(),
			})
			return
		}
		ctx.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}

	ctx.JSON(result.HTTPStatus, result)
}

// OrderStatus handles GET /checkout/orders/:id
// Used by frontend to poll the status of async (202) orders.
// This is the legacy/fallback endpoint — prefer SSE stream for real-time updates.
func (h *CheckoutHandler) OrderStatus(c context.Context, ctx *hertzApp.RequestContext) {
	orderID := ctx.Param("id")
	if orderID == "" {
		ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "order id is required",
		})
		return
	}

	status, ok := h.statusStore.Get(orderID)
	if !ok {
		ctx.JSON(http.StatusNotFound, map[string]string{
			"error": "order not found — it may have been processed synchronously",
		})
		return
	}

	ctx.JSON(http.StatusOK, status)
}

// OrderStatusStream handles GET /checkout/orders/:id/stream
//
// Server-Sent Events (SSE) endpoint that pushes real-time status updates
// for async (202) orders. This replaces polling and dramatically reduces
// server load under high QPS — the whole reason we switched to async mode.
//
// Protocol:
//   - Content-Type: text/event-stream
//   - Each status change is pushed as a "data:" SSE event (JSON)
//   - Stream closes automatically on terminal states (completed/failed)
//   - Stream closes after 60s timeout as a safety net
//   - Heartbeat ":" comments sent every 15s to keep the connection alive
//
// Why SSE instead of polling:
//
//	When QPS is high enough to trigger async mode, the last thing we want
//	is N clients polling every second — that creates MORE load than the
//	original writes. SSE pushes updates only when state actually changes,
//	reducing read QPS from O(N*polls) to O(N*state_changes) ≈ O(N*2).
//
// Why SSE instead of WebSocket:
//   - Unidirectional (server→client) — WS is overkill
//   - Standard HTTP — no protocol upgrade needed
//   - Works through HTTP/2 multiplexing
//   - Browser auto-reconnection support
//
// Implementation note:
//
//	Uses io.Pipe + ctx.SetBodyStream(-1) for chunked streaming in Hertz.
//	A background goroutine subscribes to OrderStatusStore notifications
//	and writes SSE events to the pipe writer. Hertz reads from the pipe
//	reader and sends chunks to the client. When the goroutine returns,
//	it closes the pipe writer, which signals Hertz to close the response.
func (h *CheckoutHandler) OrderStatusStream(c context.Context, ctx *hertzApp.RequestContext) {
	orderID := ctx.Param("id")
	if orderID == "" {
		ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "order id is required",
		})
		return
	}

	// Check that the order exists before establishing the SSE connection
	currentStatus, exists := h.statusStore.Get(orderID)
	if !exists {
		ctx.JSON(http.StatusNotFound, map[string]string{
			"error": "order not found — it may have been processed synchronously",
		})
		return
	}

	// Set SSE headers
	ctx.Response.Header.Set("Content-Type", "text/event-stream")
	ctx.Response.Header.Set("Cache-Control", "no-cache")
	ctx.Response.Header.Set("Connection", "keep-alive")
	ctx.Response.Header.Set("X-Accel-Buffering", "no") // disable nginx/proxy buffering
	ctx.SetStatusCode(http.StatusOK)

	// Create a pipe: the writer writes SSE events, the reader feeds Hertz.
	// SetBodyStream(-1) → chunked transfer encoding (unknown length).
	pr, pw := io.Pipe()
	ctx.SetBodyStream(pr, -1)

	// Background goroutine: subscribe to status changes and write SSE events
	go func() {
		defer pw.Close()

		ch := h.statusStore.Subscribe(orderID)
		defer h.statusStore.Unsubscribe(orderID, ch)

		// Send the current status immediately so the client doesn't miss
		// any updates that happened between POST /checkout and SSE connect
		if err := writeSSEEvent(pw, currentStatus); err != nil {
			return
		}

		// If already in a terminal state, close immediately
		if currentStatus.Status == "completed" || currentStatus.Status == "failed" {
			return
		}

		// Stream updates until terminal state, timeout, or client disconnect
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		timeout := time.After(60 * time.Second)

		for {
			select {
			case status, ok := <-ch:
				if !ok {
					// Channel closed (unsubscribed)
					return
				}
				if err := writeSSEEvent(pw, status); err != nil {
					return // client disconnected (broken pipe)
				}
				// Terminal states — close the stream
				if status.Status == "completed" || status.Status == "failed" {
					return
				}

			case <-heartbeat.C:
				// SSE heartbeat — a comment line keeps the connection alive
				// through proxies and load balancers that may timeout idle conns
				if _, err := fmt.Fprintf(pw, ": heartbeat\n\n"); err != nil {
					return
				}

			case <-timeout:
				// Safety net: close after 60s to prevent connection leaks
				writeSSENamedEvent(pw, "timeout", `{"error":"stream_timeout"}`)
				return
			}
		}
	}()
}

// writeSSEEvent marshals the status to JSON and writes it as an SSE data event.
func writeSSEEvent(w io.Writer, status infra.OrderStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

// writeSSENamedEvent writes an SSE event with a custom event name.
func writeSSENamedEvent(w io.Writer, event string, data string) error {
	_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	return err
}

// Stats handles GET /checkout/stats (admin/debug)
// Returns QPS monitoring data and current threshold.
func (h *CheckoutHandler) Stats(c context.Context, ctx *hertzApp.RequestContext) {
	stats := h.dispatcher.QPSStats()
	ctx.JSON(http.StatusOK, map[string]interface{}{
		"qps":          stats,
		"threshold":    h.dispatcher.GetThreshold(),
		"is_high_load": h.dispatcher.IsHighLoad(),
	})
}

// SetThreshold handles PUT /checkout/threshold (admin)
// Allows runtime adjustment of the QPS threshold for sync/async switching.
func (h *CheckoutHandler) SetThreshold(c context.Context, ctx *hertzApp.RequestContext) {
	var body struct {
		Threshold int `json:"threshold"`
	}
	if err := ctx.BindJSON(&body); err != nil || body.Threshold <= 0 {
		ctx.JSON(http.StatusBadRequest, map[string]string{
			"error": "threshold must be > 0",
		})
		return
	}
	h.dispatcher.SetThreshold(body.Threshold)
	ctx.JSON(http.StatusOK, map[string]interface{}{
		"message":   "threshold updated",
		"threshold": body.Threshold,
	})
}

// isSlotError checks if the error is related to product slot exhaustion.
func isSlotError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return containsStr(msg, "no available slots") ||
		containsStr(msg, "product purchase failed") ||
		containsStr(msg, "not available")
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
