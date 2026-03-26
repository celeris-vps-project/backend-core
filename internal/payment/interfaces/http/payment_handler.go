// Package http provides the payment HTTP handler that orchestrates the
// payment flow: create charge → (async webhook) → activate order → provision.
package http

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	paymentInfra "backend-core/internal/payment/infra"
	"backend-core/pkg/apperr"
	"encoding/json"
	"io"
	"log"
	"time"

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
	provider     domain.PaymentProvider       // for webhook verification
	cryptoProv   domain.CryptoPaymentProvider // for crypto-specific operations (may be nil)
	providerSvc  *paymentApp.ProviderAppService // dynamic provider management (may be nil)
}

// NewPaymentHandler constructs a PaymentHandler with the payment service,
// the cross-domain orchestrator, and the payment provider for webhook verification.
func NewPaymentHandler(
	paymentSvc *paymentApp.PaymentAppService,
	orchestrator *paymentApp.PostPaymentOrchestrator,
	provider domain.PaymentProvider,
) *PaymentHandler {
	h := &PaymentHandler{
		paymentSvc:   paymentSvc,
		orchestrator: orchestrator,
		provider:     provider,
	}
	// If the provider also implements CryptoPaymentProvider, wire it up
	if cp, ok := provider.(domain.CryptoPaymentProvider); ok {
		h.cryptoProv = cp
	}
	return h
}

// SetProviderService attaches the dynamic provider management service.
func (h *PaymentHandler) SetProviderService(svc *paymentApp.ProviderAppService) {
	h.providerSvc = svc
}

// ── Pay Request/Response types ─────────────────────────────────────────

// PayRequest is the optional JSON body for POST /orders/:id/pay.
// If network is provided, a crypto (USDT) payment is initiated.
type PayRequest struct {
	Network  string `json:"network,omitempty"`  // e.g. "arbitrum", "solana"
	Currency string `json:"currency,omitempty"` // default: "USDT"
}

// PayResponse is the response returned after initiating a payment.
type PayResponse struct {
	OrderID    string                     `json:"order_id"`
	InvoiceID  string                     `json:"invoice_id,omitempty"`
	ChargeID   string                     `json:"charge_id"`
	Status     string                     `json:"status"`
	PaymentURL string                     `json:"payment_url"`
	Message    string                     `json:"message"`
	Crypto     *domain.CryptoChargeDetail `json:"crypto,omitempty"`
}

// Pay handles POST /orders/:id/pay
//
// Flow:
//  1. Look up the order via orchestrator (must be "pending")
//  2. Parse optional request body for network selection
//  3. If crypto network specified → create crypto charge
//     Otherwise → create standard charge (legacy mock)
//  4. Create invoice, schedule timeout
//  5. Return charge result with crypto details (wallet, QR, etc.)
func (h *PaymentHandler) Pay(ctx context.Context, c *hz_app.RequestContext) {
	orderID := c.Param("id")

	// 1. Look up order (via orchestrator to avoid direct ordering import)
	order, err := h.orchestrator.GetOrderForPay(orderID)
	if err != nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeOrderNotFound, "order not found: "+err.Error()))
		return
	}
	if order.Status != "pending" {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(apperr.CodeOrderNotPending, "order is not in pending status, current: "+order.Status))
		return
	}

	// 2. Parse optional request body for network selection
	var req PayRequest
	_ = c.BindJSON(&req) // ignore errors — all fields are optional

	// 3. Create invoice
	invoiceID, err := h.orchestrator.CreateInvoiceForPayment(order)
	if err != nil {
		log.Printf("[PaymentHandler] WARNING: invoice creation failed for order %s: %v", orderID, err)
	}

	// 4. Create charge (crypto or legacy)
	var chargeResult *domain.ChargeResult

	if req.Network != "" && h.cryptoProv != nil {
		// Crypto payment with specific network
		if !domain.ValidNetwork(req.Network) {
			c.JSON(consts.StatusBadRequest, utils.H{
				"code":               apperr.CodeNetworkUnsupported,
				"error":              "unsupported network: " + req.Network,
				"supported_networks": []string{"arbitrum", "solana", "trc20", "bsc", "polygon"},
			})
			return
		}
		chargeResult, err = h.cryptoProv.CreateCryptoCharge(
			orderID,
			order.PriceAmount,
			domain.CryptoNetwork(req.Network),
		)
	} else if h.cryptoProv != nil {
		// Crypto provider available but no network specified → default to Arbitrum
		chargeResult, err = h.cryptoProv.CreateCryptoCharge(
			orderID,
			order.PriceAmount,
			domain.NetworkArbitrum,
		)
	} else {
		// Legacy mock payment
		chargeResult, err = h.paymentSvc.Process(orderID, order.Currency, order.PriceAmount)
	}

	if err != nil {
		h.orchestrator.VoidInvoiceOnFailure(invoiceID, "payment charge creation failed: "+err.Error())
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(apperr.CodePaymentFailed, "payment failed: "+err.Error()))
		return
	}

	log.Printf("[PaymentHandler] charge created: order=%s charge=%s invoice=%s status=%s network=%s",
		orderID, chargeResult.ChargeID, invoiceID, chargeResult.Status, req.Network)

	// 5. Schedule invoice timeout
	h.orchestrator.ScheduleInvoiceTimeout(invoiceID, orderID, 30*time.Minute)

	// 6. Build response
	resp := PayResponse{
		OrderID:    orderID,
		InvoiceID:  invoiceID,
		ChargeID:   chargeResult.ChargeID,
		Status:     chargeResult.Status,
		PaymentURL: chargeResult.PaymentURL,
		Message:    "payment initiated, awaiting confirmation",
		Crypto:     chargeResult.Crypto,
	}

	c.JSON(consts.StatusOK, utils.H{"data": resp})
}

// ── Networks endpoint ──────────────────────────────────────────────────

// Networks handles GET /api/v1/payment/networks
// Returns all supported blockchain networks for USDT payments.
func (h *PaymentHandler) Networks(ctx context.Context, c *hz_app.RequestContext) {
	if h.cryptoProv == nil {
		c.JSON(consts.StatusOK, utils.H{
			"data":    []interface{}{},
			"message": "crypto payments not configured",
		})
		return
	}

	networks := h.cryptoProv.GetNetworks()
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

	if h.cryptoProv == nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeCryptoNotConfigured, "crypto payments not configured"))
		return
	}

	detail := h.cryptoProv.GetChargeDetail(chargeID)
	if detail == nil {
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeChargeNotFound, "charge not found"))
		return
	}

	c.JSON(consts.StatusOK, utils.H{"data": detail})
}

// ── Webhook endpoints (unchanged) ──────────────────────────────────────

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

	payload, err := h.provider.VerifyWebhook(rawBody, signature)
	if err != nil {
		log.Printf("[PaymentHandler.Webhook] verification failed: %v", err)
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeWebhookFailed, "webhook verification failed"))
		return
	}

	log.Printf("[PaymentHandler.Webhook] received: charge=%s order=%s status=%s", payload.ChargeID, payload.OrderID, payload.Status)

	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.Webhook] payment not successful (status=%s), skipping activation", payload.Status)
		c.JSON(consts.StatusOK, utils.H{"message": "noted, payment not successful"})
		return
	}

	if err := h.orchestrator.HandlePaymentConfirmed(payload.OrderID); err != nil {
		log.Printf("[PaymentHandler.Webhook] WARNING: post-payment flow failed: %v", err)
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, "post-payment flow failed: "+err.Error()))
		return
	}

	log.Printf("[PaymentHandler.Webhook] flow complete: order=%s charge=%s", payload.OrderID, payload.ChargeID)
	c.JSON(consts.StatusOK, utils.H{"message": "payment confirmed, order activated, provisioning triggered"})
}

// HandleWebhookPayload processes a webhook payload directly (called by mock/crypto provider callback).
func (h *PaymentHandler) HandleWebhookPayload(payload *domain.WebhookPayload) {
	log.Printf("[PaymentHandler.HandleWebhookPayload] processing: charge=%s order=%s status=%s", payload.ChargeID, payload.OrderID, payload.Status)

	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.HandleWebhookPayload] payment not successful (status=%s), skipping", payload.Status)
		return
	}

	if err := h.orchestrator.HandlePaymentConfirmed(payload.OrderID); err != nil {
		log.Printf("[PaymentHandler.HandleWebhookPayload] WARNING: post-payment flow failed: %v", err)
	}
}

// CustomWebhook handles POST /api/v1/payments/webhook/custom/:providerId
//
// This endpoint receives callbacks from third-party payment gateways configured
// as "custom" providers. It:
//  1. Looks up the provider config by ID
//  2. Verifies the webhook signature using the provider's merchant_key
//  3. Parses the payload and triggers the post-payment flow
//
// The third-party gateway should POST JSON with at least:
//
//	{ "order_id": "...", "status": "success", "sign": "<signature>" }
func (h *PaymentHandler) CustomWebhook(ctx context.Context, c *hz_app.RequestContext) {
	providerID := c.Param("providerId")
	if providerID == "" {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "provider ID is required"))
		return
	}

	// 1. Look up the provider config
	if h.providerSvc == nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, "provider service not configured"))
		return
	}
	providerCfg, err := h.providerSvc.GetProvider(providerID)
	if err != nil {
		log.Printf("[PaymentHandler.CustomWebhook] provider not found: id=%s err=%v", providerID, err)
		c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeProviderNotFound, "provider not found"))
		return
	}
	if providerCfg.Type != "custom" {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "provider is not a custom type"))
		return
	}
	if !providerCfg.Enabled {
		c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(apperr.CodeInvalidParams, "provider is disabled"))
		return
	}

	// 2. Build a temporary CustomPaymentProvider to verify the webhook
	rawBody := c.Request.Body()
	signature := string(c.GetHeader("X-Webhook-Signature"))

	customProv := paymentInfra.NewCustomPaymentProvider(providerCfg, nil)
	payload, err := customProv.VerifyWebhook(rawBody, signature)
	if err != nil {
		log.Printf("[PaymentHandler.CustomWebhook] verification failed: provider=%s err=%v", providerID, err)
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeWebhookFailed, "webhook verification failed: "+err.Error()))
		return
	}

	log.Printf("[PaymentHandler.CustomWebhook] received: provider=%s charge=%s order=%s status=%s",
		providerID, payload.ChargeID, payload.OrderID, payload.Status)

	// 3. Process the payment confirmation
	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.CustomWebhook] payment not successful (status=%s), skipping activation", payload.Status)
		c.JSON(consts.StatusOK, utils.H{"message": "noted, payment not successful"})
		return
	}

	if err := h.orchestrator.HandlePaymentConfirmed(payload.OrderID); err != nil {
		log.Printf("[PaymentHandler.CustomWebhook] WARNING: post-payment flow failed: %v", err)
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, "post-payment flow failed: "+err.Error()))
		return
	}

	log.Printf("[PaymentHandler.CustomWebhook] flow complete: provider=%s order=%s", providerID, payload.OrderID)
	c.JSON(consts.StatusOK, utils.H{"message": "payment confirmed, order activated"})
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

	h.HandleWebhookPayload(payload)

	c.JSON(consts.StatusOK, utils.H{"message": "webhook simulated"})
}
