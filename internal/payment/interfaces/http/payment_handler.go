// Package http provides the payment HTTP handler that delegates all business
// logic to the app layer. The handler is responsible only for HTTP
// parsing/serialisation and error mapping — no provider routing, no infra imports.
package http

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	"backend-core/pkg/apperr"
	"backend-core/pkg/authn"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"time"

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
	CouponCode string `json:"coupon_code,omitempty"` // optional activation/coupon code
	PayType    string `json:"pay_type,omitempty"`    // EPay v1 payment channel
}

// Pay handles POST /orders/:id/pay
//
// The handler only parses the HTTP request and delegates entirely to the
// app layer. All provider routing, invoice creation, and timeout scheduling
// happens inside PaymentAppService.InitiatePayment().
func (h *PaymentHandler) Pay(ctx context.Context, c *hz_app.RequestContext) {
	orderID := c.Param("id")
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, apperr.Resp(apperr.CodeUnauthorized, "unauthorized"))
		return
	}

	var req PayRequest
	_ = c.BindJSON(&req) // ignore errors — all fields are optional

	resp, err := h.paymentSvc.InitiatePayment(ctx, &paymentApp.InitiatePaymentRequest{
		OrderID:    orderID,
		CustomerID: uid.String(),
		ProviderID: req.ProviderID,
		Network:    req.Network,
		CouponCode: req.CouponCode,
		PayType:    req.PayType,
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
	headers := collectWebhookHeaders(c)

	provider := h.paymentSvc.GetDefaultProvider()
	if provider == nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, "no default provider configured"))
		return
	}

	payload, err := provider.VerifyWebhook(rawBody, headers)
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

// EPayWebhook handles /api/v1/payments/webhook/epay/:providerId callbacks.
//
// This endpoint receives callbacks from EPay (易支付) payment gateways.
// EPay v1 may send payment notifications as form POST or signed GET query
// parameters, both with an MD5 sign field.
// The handler delegates provider lookup and signature verification to the
// app layer via VerifyProviderWebhook().
func (h *PaymentHandler) EPayWebhook(ctx context.Context, c *hz_app.RequestContext) {
	providerID := c.Param("providerId")
	log.Printf("[PaymentHandler.EPayWebhook] providerId=%s", providerID)
	if providerID == "" {
		c.String(consts.StatusBadRequest, "provider ID is required")
		return
	}

	rawBody := c.Request.Body()
	if len(rawBody) == 0 {
		rawBody = c.Request.URI().QueryString()
	}
	if len(rawBody) == 0 {
		c.String(consts.StatusBadRequest, "empty webhook payload")
		return
	}
	headers := collectWebhookHeaders(c)

	// Delegate verification to app layer (no infra import needed)
	payload, err := h.paymentSvc.VerifyProviderWebhook(providerID, rawBody, headers)
	if err != nil {
		log.Printf("[PaymentHandler.EPayWebhook] verification failed: provider=%s err=%v", providerID, err)
		// EPay protocol: respond with error text
		apperr.HandleErr(c, err)
		return
	}

	log.Printf("[PaymentHandler.EPayWebhook] received: provider=%s trade_no=%s order=%s status=%s",
		providerID, payload.ChargeID, payload.OrderID, payload.Status)

	h.paymentSvc.HandleWebhookPayload(payload)

	log.Printf("[PaymentHandler.EPayWebhook] flow complete: provider=%s order=%s", providerID, payload.OrderID)

	// Return "success" as required by EPay protocol
	c.String(consts.StatusOK, "success")
}

// EPayReturn handles the browser return_url from EPay gateways.
//
// It verifies the signed query string server-side, reconciles successful
// payments through the normal webhook path, then redirects the browser to the
// provider-neutral order status page without leaking gateway parameters.
func (h *PaymentHandler) EPayReturn(ctx context.Context, c *hz_app.RequestContext) {
	providerID := c.Param("providerId")
	if providerID == "" {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "provider ID is required"))
		return
	}

	rawQuery := c.Request.URI().QueryString()
	result, err := h.paymentSvc.HandleProviderReturn(providerID, rawQuery)
	if err != nil {
		log.Printf("[PaymentHandler.EPayReturn] verification failed: provider=%s err=%v", providerID, err)
		orderID := strings.TrimSpace(c.Query("out_trade_no"))
		if resolved, resolveErr := h.paymentSvc.ResolvePaymentAttemptOrderID(providerID, orderID); resolveErr == nil {
			orderID = resolved
		}
		if orderID == "" {
			apperr.HandleErr(c, err)
			return
		}
		h.redirectToPaymentStatus(c, orderID, "")
		return
	}

	redirectResult := result.Status
	if redirectResult == domain.ChargeStatusPending {
		redirectResult = ""
	}
	h.redirectToPaymentStatus(c, result.OrderID, redirectResult)
}

// PaymentStatusStream handles GET /orders/:id/payments/status/stream.
func (h *PaymentHandler) PaymentStatusStream(ctx context.Context, c *hz_app.RequestContext) {
	orderID := c.Param("id")
	if orderID == "" {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, "order id is required"))
		return
	}
	uid, ok := authn.UserID(c)
	if !ok {
		c.JSON(consts.StatusUnauthorized, apperr.Resp(apperr.CodeUnauthorized, "unauthorized"))
		return
	}
	if err := h.paymentSvc.CanAccessOrder(orderID, uid.String()); err != nil {
		apperr.HandleErr(c, err)
		return
	}

	events, unsubscribe := h.paymentSvc.SubscribePaymentStatus(orderID)
	c.Response.Header.Set("Content-Type", "text/event-stream")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("Connection", "keep-alive")
	c.Response.Header.Set("X-Accel-Buffering", "no")
	c.SetStatusCode(consts.StatusOK)

	pr, pw := io.Pipe()
	c.SetBodyStream(pr, -1)

	go func() {
		defer pw.Close()
		defer unsubscribe()

		localStatusTicker := time.NewTicker(5 * time.Second)
		defer localStatusTicker.Stop()
		timeout := time.After(5 * time.Minute)

		for {
			select {
			case event, ok := <-events:
				if !ok {
					return
				}
				if err := writePaymentSSEEvent(pw, event); err != nil {
					return
				}
				if event.Status == domain.ChargeStatusSuccess || event.Status == domain.ChargeStatusFailed {
					return
				}
			case <-localStatusTicker.C:
				event, err := h.paymentSvc.GetLocalTerminalPaymentStatus(orderID)
				if err != nil {
					log.Printf("[PaymentHandler.PaymentStatusStream] local status check failed: order=%s err=%v", orderID, err)
				}
				if event != nil {
					if err := writePaymentSSEEvent(pw, *event); err != nil {
						return
					}
					return
				}
				if _, err := fmt.Fprintf(pw, ": heartbeat\n\n"); err != nil {
					return
				}
			case <-timeout:
				_, _ = fmt.Fprintf(pw, "event: timeout\ndata: {\"error\":\"stream_timeout\"}\n\n")
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (h *PaymentHandler) redirectToPaymentStatus(c *hz_app.RequestContext, orderID, result string) {
	target := "/payment/result/" + url.PathEscape(orderID)
	if result != "" {
		q := url.Values{}
		q.Set("result", result)
		target += "?" + q.Encode()
	}
	c.Response.Header.Set("Location", target)
	c.SetStatusCode(consts.StatusFound)
}

func writePaymentSSEEvent(w io.Writer, event paymentApp.PaymentStatusEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func collectWebhookHeaders(c *hz_app.RequestContext) domain.WebhookHeaders {
	headers := make(domain.WebhookHeaders)
	c.Request.Header.VisitAll(func(key, value []byte) {
		headers[strings.ToLower(string(key))] = string(value)
	})
	return headers
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
