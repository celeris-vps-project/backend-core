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
	provider     domain.PaymentProvider         // for webhook verification
	cryptoProv   domain.CryptoPaymentProvider   // for crypto-specific operations (may be nil)
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
// If provider_id is provided, the payment is routed to that specific provider.
type PayRequest struct {
	Type       paymentApp.PaymentType `json:"payment_type"`
	Network    string                 `json:"network,omitempty"`     // e.g. "arbitrum", "solana"
	Currency   string                 `json:"currency,omitempty"`    // default: "USDT"
	ProviderID string                 `json:"provider_id,omitempty"` // dynamic provider selection
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

	// 4. Create charge — route based on provider_id or legacy flow
	var chargeResult *domain.ChargeResult

	if req.ProviderID != "" && h.providerSvc != nil {
		// ── Dynamic provider routing ──
		providerCfg, provErr := h.providerSvc.GetProviderConfig(req.ProviderID)
		if provErr != nil {
			c.JSON(consts.StatusNotFound, apperr.Resp(apperr.CodeProviderNotFound, "payment provider not found"))
			return
		}
		if !providerCfg.Enabled {
			c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(apperr.CodeInvalidParams, "payment provider is disabled"))
			return
		}

		switch providerCfg.Type {
		case domain.ProviderTypeCryptoUSDT:
			// Crypto USDT — delegate to crypto provider with optional network
			if h.cryptoProv != nil {
				network := domain.CryptoNetwork(req.Network)
				if req.Network == "" {
					network = domain.NetworkArbitrum // default
				} else if !domain.ValidNetwork(req.Network) {
					c.JSON(consts.StatusBadRequest, utils.H{
						"code":               apperr.CodeNetworkUnsupported,
						"error":              "unsupported network: " + req.Network,
						"supported_networks": []string{"arbitrum", "solana", "trc20", "bsc", "polygon"},
					})
					return
				}
				chargeResult, err = h.cryptoProv.CreateCryptoCharge(orderID, order.PriceAmount, network)
			} else {
				c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(apperr.CodeCryptoNotConfigured, "crypto payments not configured"))
				return
			}

		case domain.ProviderTypeEPay:
			// EPay (易支付) gateway — V1 (MD5) or V2 (RSA) based on config
			epayProv := paymentInfra.NewEPayPaymentProvider(providerCfg, h.HandleWebhookPayload)
			chargeResult, err = epayProv.CreateCharge(orderID, order.Currency, order.PriceAmount)

		default:
			// To add a new payment provider, implement domain.PaymentProvider and
			// register it here.
			c.JSON(consts.StatusUnprocessableEntity, apperr.Resp(apperr.CodeInternalError,
				"payment provider type '"+providerCfg.Type+"' is not yet implemented"))
			return
		}
	} else if req.Network != "" && h.cryptoProv != nil {
		// ── Legacy flow: crypto payment with specific network (no provider_id) ──
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

// EPayWebhook handles GET /api/v1/payments/webhook/epay/:providerId
//
// This endpoint receives callbacks from EPay (易支付) payment gateways.
// EPay sends payment notifications as GET requests with query parameters:
//
//	GET /api/v1/payments/webhook/epay/{providerId}?pid=1001&trade_no=...
//	    &out_trade_no=ORDER123&type=alipay&trade_status=TRADE_SUCCESS
//	    &name=VPS+Service&money=1.00&sign=...&sign_type=MD5|RSA
//
// The handler:
//  1. Looks up the EPay provider config by ID
//  2. Passes the raw query string to VerifyWebhook for signature verification
//  3. If trade_status == TRADE_SUCCESS → triggers post-payment flow
//  4. Returns plain text "success" (as required by EPay protocol)
func (h *PaymentHandler) EPayWebhook(ctx context.Context, c *hz_app.RequestContext) {
	providerID := c.Param("providerId")
	if providerID == "" {
		c.String(consts.StatusBadRequest, "provider ID is required")
		return
	}

	// 1. Look up the provider config
	if h.providerSvc == nil {
		c.String(consts.StatusInternalServerError, "provider service not configured")
		return
	}
	providerCfg, err := h.providerSvc.GetProviderConfig(providerID)
	if err != nil {
		log.Printf("[PaymentHandler.EPayWebhook] provider not found: id=%s err=%v", providerID, err)
		c.String(consts.StatusNotFound, "provider not found")
		return
	}
	if providerCfg.Type != domain.ProviderTypeEPay {
		c.String(consts.StatusBadRequest, "provider is not an EPay type")
		return
	}
	if !providerCfg.Enabled {
		c.String(consts.StatusUnprocessableEntity, "provider is disabled")
		return
	}

	// 2. Extract the raw query string and verify webhook
	// EPay sends all parameters as GET query params
	rawQuery := string(c.Request.URI().QueryString())
	if rawQuery == "" {
		c.String(consts.StatusBadRequest, "empty query string")
		return
	}

	epayProv := paymentInfra.NewEPayPaymentProvider(providerCfg, nil)
	payload, err := epayProv.VerifyWebhook([]byte(rawQuery), "")
	if err != nil {
		log.Printf("[PaymentHandler.EPayWebhook] verification failed: provider=%s err=%v", providerID, err)
		c.String(consts.StatusBadRequest, "webhook verification failed: "+err.Error())
		return
	}

	log.Printf("[PaymentHandler.EPayWebhook] received: provider=%s trade_no=%s order=%s status=%s",
		providerID, payload.ChargeID, payload.OrderID, payload.Status)

	// 3. Process the payment confirmation
	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentHandler.EPayWebhook] payment not successful (status=%s), skipping activation", payload.Status)
		// EPay requires "success" response even for non-successful statuses
		// to acknowledge receipt of the notification
		c.String(consts.StatusOK, "success")
		return
	}

	if err := h.orchestrator.HandlePaymentConfirmed(payload.OrderID); err != nil {
		log.Printf("[PaymentHandler.EPayWebhook] WARNING: post-payment flow failed: %v", err)
		c.String(consts.StatusInternalServerError, "post-payment flow failed")
		return
	}

	log.Printf("[PaymentHandler.EPayWebhook] flow complete: provider=%s order=%s", providerID, payload.OrderID)

	// 4. Return "success" as required by EPay protocol
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

	h.HandleWebhookPayload(payload)

	c.JSON(consts.StatusOK, utils.H{"message": "webhook simulated"})
}
