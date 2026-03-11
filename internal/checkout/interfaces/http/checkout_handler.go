package http

import (
	"backend-core/internal/checkout/domain"
	"backend-core/internal/checkout/infra"
	"backend-core/pkg/adaptive"
	"backend-core/pkg/authn"
	"context"
	"net/http"

	hertzApp "github.com/cloudwego/hertz/pkg/app"
)

// CheckoutHandler exposes HTTP endpoints for the unified checkout module.
//
// Endpoints:
//   - POST /checkout             → adaptive sync/async checkout
//   - GET  /checkout/orders/:id  → poll async order status
//   - GET  /checkout/stats       → QPS monitor stats (admin/debug)
//   - PUT  /checkout/threshold   → update QPS threshold (admin)
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
//   - 202: order queued, poll /checkout/orders/:id for status
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
