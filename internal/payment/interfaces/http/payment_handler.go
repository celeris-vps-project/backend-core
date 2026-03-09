// Package http provides the payment HTTP handler that orchestrates the
// payment flow: create charge → (async webhook) → activate order → provision.
package http

import (
	instanceApp "backend-core/internal/instance/app"
	orderingApp "backend-core/internal/ordering/app"
	paymentApp "backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	productApp "backend-core/internal/product/app"
	"context"
	"encoding/json"
	"io"
	"log"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// PaymentHandler wires the payment flow into HTTP endpoints.
type PaymentHandler struct {
	paymentSvc  *paymentApp.PaymentAppService
	orderSvc    *orderingApp.OrderAppService
	productSvc  *productApp.ProductAppService
	instanceSvc *instanceApp.InstanceAppService // creates pending instances after payment
	provider    domain.PaymentProvider           // for webhook verification
}

func NewPaymentHandler(
	paymentSvc *paymentApp.PaymentAppService,
	orderSvc *orderingApp.OrderAppService,
	productSvc *productApp.ProductAppService,
	instanceSvc *instanceApp.InstanceAppService,
	provider domain.PaymentProvider,
) *PaymentHandler {
	return &PaymentHandler{
		paymentSvc:  paymentSvc,
		orderSvc:    orderSvc,
		productSvc:  productSvc,
		instanceSvc: instanceSvc,
		provider:    provider,
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
// New flow (decoupled from provisioning):
//  1. Look up the order (must be "pending")
//  2. Call PaymentAppService.Process() → creates charge, returns "pending"
//  3. Return the charge result to the frontend (status: pending)
//  4. The mock provider will fire an async webhook after ~2s
//  5. The Webhook endpoint handles activation + provisioning
func (h *PaymentHandler) Pay(ctx context.Context, c *hz_app.RequestContext) {
	orderID := c.Param("id")

	// 1. Look up order
	order, err := h.orderSvc.GetOrder(orderID)
	if err != nil {
		c.JSON(consts.StatusNotFound, utils.H{"error": "order not found: " + err.Error()})
		return
	}
	if order.Status() != "pending" {
		c.JSON(consts.StatusUnprocessableEntity, utils.H{"error": "order is not in pending status, current: " + order.Status()})
		return
	}

	// 2. Process payment (mock returns "pending" and fires webhook async)
	chargeResult, err := h.paymentSvc.Process(orderID, order.Currency(), order.PriceAmount())
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

// WebhookPayload is the JSON body sent by the payment gateway callback.
type WebhookRequest struct {
	ChargeID string `json:"charge_id"`
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
}

// Webhook handles POST /api/v1/payments/webhook
//
// This endpoint is called by the payment gateway (or mock provider) to confirm
// a payment. It:
//  1. Verifies the webhook signature/payload
//  2. If status == "success": activates the order + triggers provisioning
//  3. Returns 200 OK to the gateway
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

	// 2. Activate the order (pending → active)
	if err := h.orderSvc.ActivateOrder(payload.OrderID); err != nil {
		log.Printf("[PaymentHandler.Webhook] WARNING: order activation failed: %v", err)
		c.JSON(consts.StatusInternalServerError, utils.H{"error": "order activation failed: " + err.Error()})
		return
	}

	// 3. Trigger product purchase → provisioning
	order, err := h.orderSvc.GetOrder(payload.OrderID)
	if err != nil {
		log.Printf("[PaymentHandler.Webhook] WARNING: could not reload order: %v", err)
		c.JSON(consts.StatusInternalServerError, utils.H{"error": "could not reload order"})
		return
	}

	cfg := order.VPSConfig()
	product, err := h.productSvc.PurchaseProduct(
		order.ProductID(),
		order.CustomerID(),
		payload.OrderID,
		cfg.Hostname(),
		cfg.OS(),
	)
	if err != nil {
		log.Printf("[PaymentHandler.Webhook] WARNING: product purchase failed: %v", err)
		// Order is already activated — provisioning can be retried
		c.JSON(consts.StatusOK, utils.H{"message": "order activated, but provisioning failed: " + err.Error()})
		return
	}

	// 4. Create a pending instance — immediately visible to the user.
	if h.instanceSvc != nil {
		inst, err := h.instanceSvc.CreatePendingInstance(
			order.CustomerID(),
			payload.OrderID,
			product.Location(),
			cfg.Hostname(),
			product.Slug(),
			cfg.OS(),
			product.CPU(),
			product.MemoryMB(),
			product.DiskGB(),
		)
		if err != nil {
			log.Printf("[PaymentHandler.Webhook] WARNING: create pending instance failed: %v", err)
		} else {
			log.Printf("[PaymentHandler.Webhook] pending instance created: %s", inst.ID())
		}
	}

	log.Printf("[PaymentHandler.Webhook] flow complete: order=%s charge=%s → activated → provisioned", payload.OrderID, payload.ChargeID)
	c.JSON(consts.StatusOK, utils.H{"message": "payment confirmed, order activated, provisioning triggered"})
}

// InternalWebhookHandler processes a webhook payload directly (called by mock provider callback).
// This avoids the mock needing to make an HTTP call to itself.
func (h *PaymentHandler) HandleWebhookPayload(payload *domain.WebhookPayload) {
	log.Printf("[PaymentHandler.HandleWebhookPayload] processing: charge=%s order=%s status=%s", payload.ChargeID, payload.OrderID, payload.Status)

	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.HandleWebhookPayload] payment not successful (status=%s), skipping", payload.Status)
		return
	}

	// Activate the order
	if err := h.orderSvc.ActivateOrder(payload.OrderID); err != nil {
		log.Printf("[PaymentHandler.HandleWebhookPayload] WARNING: order activation failed: %v", err)
		return
	}

	// Trigger product purchase → provisioning
	order, err := h.orderSvc.GetOrder(payload.OrderID)
	if err != nil {
		log.Printf("[PaymentHandler.HandleWebhookPayload] WARNING: could not reload order: %v", err)
		return
	}

	cfg := order.VPSConfig()
	product, err := h.productSvc.PurchaseProduct(
		order.ProductID(),
		order.CustomerID(),
		payload.OrderID,
		cfg.Hostname(),
		cfg.OS(),
	)
	if err != nil {
		log.Printf("[PaymentHandler.HandleWebhookPayload] WARNING: product purchase failed: %v", err)
		return
	}

	// Create a pending instance — immediately visible to the user.
	// The provisioning bus will handle async provisioning (mock or real agent).
	if h.instanceSvc != nil {
		inst, err := h.instanceSvc.CreatePendingInstance(
			order.CustomerID(),
			payload.OrderID,
			product.Location(),
			cfg.Hostname(),
			product.Slug(),
			cfg.OS(),
			product.CPU(),
			product.MemoryMB(),
			product.DiskGB(),
		)
		if err != nil {
			log.Printf("[PaymentHandler.HandleWebhookPayload] WARNING: create pending instance failed: %v", err)
		} else {
			log.Printf("[PaymentHandler.HandleWebhookPayload] pending instance created: %s", inst.ID())
		}
	}

	log.Printf("[PaymentHandler.HandleWebhookPayload] flow complete: order=%s → activated → provisioned", payload.OrderID)
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
