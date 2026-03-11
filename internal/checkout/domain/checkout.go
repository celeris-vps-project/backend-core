// Package domain defines the core types for the unified checkout module.
//
// The checkout module is a cross-domain orchestration layer that combines
// the adaptive QPS-based sync/async switching (from pkg/adaptive) with
// real business logic from the product, ordering, and billing domains.
//
// Unlike the original flash-sale module which used mock payments and
// in-memory stock counters, the checkout module delegates to real domain
// services for inventory management and order creation.
package domain

// CheckoutRequest carries all information needed to process a purchase.
// This is the unified request type used for both normal and high-load paths.
type CheckoutRequest struct {
	ProductID  string `json:"product_id"`
	CustomerID string `json:"customer_id"`
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
}

// CheckoutResult represents the outcome of a checkout attempt.
// HTTPStatus distinguishes synchronous success (200) from asynchronous
// queuing (202), enabling the frontend to adapt its UX accordingly.
//
// The frontend should:
//   - On 200: show "Purchase Successful" immediately
//   - On 202: show "Queued" and poll GET /checkout/orders/:id/status
type CheckoutResult struct {
	HTTPStatus int    `json:"http_status"` // 200 = done, 202 = queued
	OrderID    string `json:"order_id"`
	Message    string `json:"message"`
	QueuePos   int    `json:"queue_pos,omitempty"` // position in queue (202 only)
}
