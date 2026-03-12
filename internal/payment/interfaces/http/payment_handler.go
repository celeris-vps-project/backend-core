// Package http provides the payment HTTP handler that orchestrates the
// payment flow: create charge → (async webhook) → activate order → provision.
package http

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	"encoding/json"
	"io"
	"log"

	"context"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// PaymentHandler wires the payment flow into HTTP endpoints.
// It calls only its own PaymentAppService and the PostPaymentOrchestrator —
// cross-domain orchestration is fully encapsulated in the orchestrator.
type PaymentHandler struct {
	paymentSvc   *paymentApp.PaymentAppService
	orchestrator *paymentApp.PostPaymentOrchestrator
	provider     domain.PaymentProvider // for webhook verification
}

// NewPaymentHandler constructs a PaymentHandler with the payment service,
// the cross-domain orchestrator, and the payment provider for webhook verification.
func NewPaymentHandler(
	paymentSvc *paymentApp.PaymentAppService,
	orchestrator *paymentApp.PostPaymentOrchestrator,
	provider domain.PaymentProvider,
) *PaymentHandler {
	return &PaymentHandler{
		paymentSvc:   paymentSvc,
		orchestrator: orchestrator,
		provider:     provider,
	}
}

// PayResponse is the response returned after initiating a payment.
type PayResponse struct {
	OrderID    string `json:"order_id"`
	ChargeID   string `json:"charge_id"`
	Status     string `json:"status"`      // "pending" — waiting for webhook
	PaymentURL string `json:"payment_url"` // redirect URL (frontend checkout page)
	Message    string `json:"message"`
}

// Pay handles POST /orders/:id/pay
//
// Flow:
//  1. Look up the order via orchestrator (must be "pending")
//  2. Call PaymentAppService.Process() → creates charge, returns "pending"
//  3. Return the charge result to the frontend (status: pending)
//  4. The mock provider fires an async webhook after ~2s
//  5. The Webhook endpoint handles activation + provisioning via orchestrator
func (h *PaymentHandler) Pay(ctx context.Context, c *hz_app.RequestContext) {
	orderID := c.Param("id")

	// 1. Look up order (via orchestrator to avoid direct ordering import)
	order, err := h.orchestrator.GetOrderForPay(orderID)
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": "order not found: " + err.Error()})
		return
	}
	if order.Status != "pending" {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": "order is not in pending status, current: " + order.Status})
		return
	}

	// 2. Process payment (mock returns "pending" and fires webhook async)
	chargeResult, err := h.paymentSvc.Process(orderID, order.Currency, order.PriceAmount)
	if err != nil {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": "payment failed: " + err.Error()})
		return
	}

	log.Printf("[PaymentHandler] charge created: order=%s charge=%s status=%s", orderID, chargeResult.ChargeID, chargeResult.Status)

	// 3. Return pending status — frontend will poll order status
	c.JSON(consts.StatusOK, utils.H{
		"data": PayResponse{
			OrderID:    orderID,
			ChargeID:   chargeResult.ChargeID,
			Status:     chargeResult.Status,
			PaymentURL: chargeResult.PaymentURL,
			Message:    "payment initiated, awaiting confirmation",
		},
	})
}

// WebhookRequest is the JSON body sent by the payment gateway callback.
type WebhookRequest struct {
	ChargeID string `json:"charge_id"`
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
}

// Webhook handles POST /api/v1/payments/webhook
//
// This endpoint is called by the payment gateway (or mock provider) to confirm
// a payment. It verifies the webhook and delegates post-payment processing to
// the orchestrator.
func (h *PaymentHandler) Webhook(ctx context.Context, c *hz_app.RequestContext) {
	rawBody := c.Request.Body()
	signature := string(c.GetHeader("X-Webhook-Signature"))

	// 1. Verify webhook authenticity
	payload, err := h.provider.VerifyWebhook(rawBody, signature)
	if err != nil {
		log.Printf("[PaymentHandler.Webhook] verification failed: %v", err)
		c.JSON(consts.StatusBadRequest, utils.H{"error": "webhook verification failed"})
		return
	}

	log.Printf("[PaymentHandler.Webhook] received: charge=%s order=%s status=%s", payload.ChargeID, payload.OrderID, payload.Status)

	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.Webhook] payment not successful (status=%s), skipping activation", payload.Status)
		c.JSON(consts.StatusOK, utils.H{"message": "noted, payment not successful"})
		return
	}

	// 2. Delegate all post-payment cross-domain work to the orchestrator
	if err := h.orchestrator.HandlePaymentConfirmed(payload.OrderID); err != nil {
		log.Printf("[PaymentHandler.Webhook] WARNING: post-payment flow failed: %v", err)
		c.JSON(consts.StatusInternalServerError, utils.H{"error": "post-payment flow failed: " + err.Error()})
		return
	}

	log.Printf("[PaymentHandler.Webhook] flow complete: order=%s charge=%s", payload.OrderID, payload.ChargeID)
	c.JSON(consts.StatusOK, utils.H{"message": "payment confirmed, order activated, provisioning triggered"})
}

// HandleWebhookPayload processes a webhook payload directly (called by mock provider callback).
// This avoids the mock needing to make an HTTP call to itself.
func (h *PaymentHandler) HandleWebhookPayload(payload *domain.WebhookPayload) {
	log.Printf("[PaymentHandler.HandleWebhookPayload] processing: charge=%s order=%s status=%s", payload.ChargeID, payload.OrderID, payload.Status)

	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.HandleWebhookPayload] payment not successful (status=%s), skipping", payload.Status)
		return
	}

	// Delegate all cross-domain post-payment logic to the orchestrator
	if err := h.orchestrator.HandlePaymentConfirmed(payload.OrderID); err != nil {
		log.Printf("[PaymentHandler.HandleWebhookPayload] WARNING: post-payment flow failed: %v", err)
	}
}

// SimulateWebhook handles POST /api/v1/payments/webhook/simulate
// This is a convenience endpoint for development/testing that lets the frontend
// or a test script manually trigger the webhook flow.
func (h *PaymentHandler) SimulateWebhook(ctx context.Context, c *hz_app.RequestContext) {
	body, err := io.ReadAll(c.Request.BodyStream())
	if err != nil {
		body = c.Request.Body()
	}

	var req WebhookRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(consts.StatusBadRequest, utils.H{"error": "invalid JSON body"})
		return
	}

	if req.OrderID == "" {
		c.JSON(consts.StatusBadRequest, utils.H{"error": "order_id is required"})
		return
	}

	payload := &domain.WebhookPayload{
		ChargeID:  req.ChargeID,
		OrderID:   req.OrderID,
		Status:    req.Status,
		RawBody:   body,
		Signature: "simulated",
	}

	h.HandleWebhookPayload(payload)

	c.JSON(consts.StatusOK, utils.H{"message": "webhook simulated"})
}
