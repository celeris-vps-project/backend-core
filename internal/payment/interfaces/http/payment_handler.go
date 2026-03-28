// Package http provides the payment HTTP handler that delegates all business
// logic to the app layer. The handler is responsible only for HTTP
// parsing/serialisation and error mapping — no provider routing, no infra imports.
package http

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	"backend-core/pkg/apperr"
	"encoding/json"
	"io"
	"log"

	"context"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// PaymentHandler wires the payment flow into HTTP endpoints.
// It calls only PaymentAppService — all cross-domain orchestration
// and provider routing is encapsulated in the app layer.
type PaymentHandler struct {
	paymentSvc *paymentApp.PaymentAppService
}

// NewPaymentHandler constructs a PaymentHandler.
func NewPaymentHandler(paymentSvc *paymentApp.PaymentAppService) *PaymentHandler {
	return &PaymentHandler{paymentSvc: paymentSvc}
}

// ── Pay Request type ───────────────────────────────────────────────────

// PayRequest is the optional JSON body for POST /orders/:id/pay.
type PayRequest struct {
	Network    string `json:"network,omitempty"`     // e.g. "arbitrum", "solana"
	ProviderID string `json:"provider_id,omitempty"` // dynamic provider selection
}

// Pay handles POST /orders/:id/pay
//
// The handler only parses the HTTP request and delegates entirely to the
// app layer. All provider routing, invoice creation, and timeout scheduling
// happens inside PaymentAppService.InitiatePayment().
func (h *PaymentHandler) Pay(ctx context.Context, c *hz_app.RequestContext) {
	orderID := c.Param("id")

	var req PayRequest
	_ = c.BindJSON(&req) // ignore errors — all fields are optional

	resp, err := h.paymentSvc.InitiatePayment(&paymentApp.InitiatePaymentRequest{
		OrderID:    orderID,
		ProviderID: req.ProviderID,
		Network:    req.Network,
	})
	if err != nil {
		apperr.HandleErr(c, err)
		return
	}

	c.JSON(consts.StatusOK, utils.H{"data": resp})
}

// ── Networks endpoint ──────────────────────────────────────────────────

// Networks handles GET /api/v1/payment/networks
// Returns all supported blockchain networks for USDT payments.
func (h *PaymentHandler) Networks(ctx context.Context, c *hz_app.RequestContext) {
	networks := h.paymentSvc.GetCryptoNetworks()
	if networks == nil {
		c.JSON(consts.StatusOK, utils.H{
			"data":    []interface{}{},
			"message": "crypto payments not configured",
		})
		return
	}

	c.JSON(consts.StatusOK, utils.H{
		"data":     networks,
		"currency": "USDT",
	})
}

// ── Charge Detail endpoint ─────────────────────────────────────────────

// ChargeDetail handles GET /api/v1/payment/charges/:id
// Returns the crypto-specific details for a pending charge (wallet, QR, expiry).
func (h *PaymentHandler) ChargeDetail(ctx context.Context, c *hz_app.RequestContext) {
	chargeID := c.Param("id")

	detail := h.paymentSvc.GetCryptoChargeDetail(chargeID)
	if detail == nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeChargeNotFound, "charge not found"))
		return
	}

	c.JSON(consts.StatusOK, utils.H{"data": detail})
}

// ── Webhook endpoints ──────────────────────────────────────────────────

// WebhookRequest is the JSON body sent by the payment gateway callback.
type WebhookRequest struct {
	ChargeID string `json:"charge_id"`
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
}

// Webhook handles POST /api/v1/payments/webhook
func (h *PaymentHandler) Webhook(ctx context.Context, c *hz_app.RequestContext) {
	rawBody := c.Request.Body()
	signature := string(c.GetHeader("X-Webhook-Signature"))

	provider := h.paymentSvc.GetDefaultProvider()
	if provider == nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, "no default provider configured"))
		return
	}

	payload, err := provider.VerifyWebhook(rawBody, signature)
	if err != nil {
		log.Printf("[PaymentHandler.Webhook] verification failed: %v", err)
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeWebhookFailed, "webhook verification failed"))
		return
	}

	log.Printf("[PaymentHandler.Webhook] received: charge=%s order=%s status=%s",
		payload.ChargeID, payload.OrderID, payload.Status)

	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.Webhook] payment not successful (status=%s), skipping activation", payload.Status)
		c.JSON(consts.StatusOK, utils.H{"message": "noted, payment not successful"})
		return
	}

	h.paymentSvc.HandleWebhookPayload(payload)

	log.Printf("[PaymentHandler.Webhook] flow complete: order=%s charge=%s", payload.OrderID, payload.ChargeID)
	c.JSON(consts.StatusOK, utils.H{"message": "payment confirmed, order activated, provisioning triggered"})
}

// EPayWebhook handles GET /api/v1/payments/webhook/epay/:providerId
//
// This endpoint receives callbacks from EPay (易支付) payment gateways.
// EPay sends payment notifications as GET requests with query parameters.
// The handler delegates provider lookup and signature verification to the
// app layer via VerifyProviderWebhook().
func (h *PaymentHandler) EPayWebhook(ctx context.Context, c *hz_app.RequestContext) {
	providerID := c.Param("providerId")
	if providerID == "" {
		c.String(consts.StatusBadRequest, "provider ID is required")
		return
	}

	// Extract the raw query string — EPay sends all parameters as GET query params
	rawQuery := string(c.Request.URI().QueryString())
	if rawQuery == "" {
		c.String(consts.StatusBadRequest, "empty query string")
		return
	}

	// Delegate verification to app layer (no infra import needed)
	payload, err := h.paymentSvc.VerifyProviderWebhook(providerID, []byte(rawQuery), "")
	if err != nil {
		log.Printf("[PaymentHandler.EPayWebhook] verification failed: provider=%s err=%v", providerID, err)
		// EPay protocol: respond with error text
		apperr.HandleErr(c, err)
		return
	}

	log.Printf("[PaymentHandler.EPayWebhook] received: provider=%s trade_no=%s order=%s status=%s",
		providerID, payload.ChargeID, payload.OrderID, payload.Status)

	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.EPayWebhook] payment not successful (status=%s), skipping", payload.Status)
		// EPay requires "success" response to acknowledge receipt
		c.String(consts.StatusOK, "success")
		return
	}

	h.paymentSvc.HandleWebhookPayload(payload)

	log.Printf("[PaymentHandler.EPayWebhook] flow complete: provider=%s order=%s", providerID, payload.OrderID)

	// Return "success" as required by EPay protocol
	c.String(consts.StatusOK, "success")
}

// SimulateWebhook handles POST /api/v1/payments/webhook/simulate
func (h *PaymentHandler) SimulateWebhook(ctx context.Context, c *hz_app.RequestContext) {
	body, err := io.ReadAll(c.Request.BodyStream())
	if err != nil {
		body = c.Request.Body()
	}

	var req WebhookRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "invalid JSON body"))
		return
	}

	if req.OrderID == "" {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "order_id is required"))
		return
	}

	payload := &domain.WebhookPayload{
		ChargeID:  req.ChargeID,
		OrderID:   req.OrderID,
		Status:    req.Status,
		RawBody:   body,
		Signature: "simulated",
	}

	h.paymentSvc.HandleWebhookPayload(payload)

	c.JSON(consts.StatusOK, utils.H{"message": "webhook simulated"})
}
