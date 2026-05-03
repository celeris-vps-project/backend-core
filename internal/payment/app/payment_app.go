package app

import (
	"backend-core/internal/payment/domain"
	"backend-core/pkg/apperr"
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ── Request / Response DTOs ────────────────────────────────────────────

// InitiatePaymentRequest is the app-layer input for starting a payment.
type InitiatePaymentRequest struct {
	OrderID    string // required — the order to pay for
	ProviderID string // optional — dynamic provider selection
	Network    string // optional — crypto network (e.g. "arbitrum")
	CouponCode string // optional — activation/coupon code for first payment
	PayType    string // optional EPay v1 payment channel
}

// InitiatePaymentResponse is the app-layer output after initiating a payment.
type InitiatePaymentResponse struct {
	OrderID        string                     `json:"order_id"`
	InvoiceID      string                     `json:"invoice_id,omitempty"`
	ChargeID       string                     `json:"charge_id"`
	Status         string                     `json:"status"`
	PaymentURL     string                     `json:"payment_url"`
	Message        string                     `json:"message"`
	PayableAmount  int64                      `json:"payable_amount"`
	DiscountAmount int64                      `json:"discount_amount,omitempty"`
	CouponID       string                     `json:"coupon_id,omitempty"`
	Crypto         *domain.CryptoChargeDetail `json:"crypto,omitempty"`
}

type PaymentStatusEvent struct {
	OrderID  string `json:"order_id"`
	ChargeID string `json:"charge_id,omitempty"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
}

type ProviderReturnResult struct {
	OrderID string
	Status  string
}

type CouponApplicationRequest struct {
	Code           string
	UserID         string
	OrderID        string
	ProductID      string
	OriginalAmount int64
}

type CouponApplicationResult struct {
	Applied        bool
	CouponID       string
	Code           string
	DiscountAmount int64
	FinalAmount    int64
}

type CouponApplier interface {
	ApplyCoupon(ctx context.Context, req CouponApplicationRequest) (CouponApplicationResult, error)
}

// ── Service ────────────────────────────────────────────────────────────

// PaymentAppService orchestrates the payment initiation flow:
//   - Look up order (via orchestrator)
//   - Create invoice (via orchestrator)
//   - Route to correct provider and create charge
//   - Schedule invoice timeout
//
// All provider routing, invoice lifecycle, and timeout scheduling logic
// lives here — the handler layer is kept thin (HTTP parse → call → respond).
type PaymentAppService struct {
	providerSvc  *ProviderAppService
	orchestrator *PostPaymentOrchestrator
	cryptoProv   domain.CryptoPaymentProvider // optional legacy crypto provider
	renewals     *RenewalService
	coupons      CouponApplier

	statusMu          sync.RWMutex
	statusSubscribers map[string]map[chan PaymentStatusEvent]struct{}
}

func NewPaymentAppService(
	providerSvc *ProviderAppService,
	orchestrator *PostPaymentOrchestrator,
	cryptoProv domain.CryptoPaymentProvider,
) *PaymentAppService {
	return &PaymentAppService{
		providerSvc:       providerSvc,
		orchestrator:      orchestrator,
		cryptoProv:        cryptoProv,
		statusSubscribers: make(map[string]map[chan PaymentStatusEvent]struct{}),
	}
}

func (s *PaymentAppService) SetRenewalService(renewals *RenewalService) {
	s.renewals = renewals
}

func (s *PaymentAppService) SetCouponApplier(coupons CouponApplier) {
	s.coupons = coupons
}

// InitiatePayment is the single entry-point for starting a payment.
// It handles order lookup, invoice creation, provider routing, charge
// creation, and timeout scheduling.
//
// Returns *AppError for all known business errors so the handler can
// call apperr.HandleErr() without any business logic.
func (s *PaymentAppService) InitiatePayment(ctx context.Context, req *InitiatePaymentRequest) (*InitiatePaymentResponse, error) {
	if req.OrderID == "" {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "order_id is required")
	}

	// 1. Look up the order
	order, err := s.orchestrator.GetOrderForPay(req.OrderID)
	if err != nil {
		return nil, apperr.ErrNotFound(apperr.CodeOrderNotFound, "order not found: "+err.Error())
	}
	invoiceID := ""
	payableOrder := order
	couponResult := CouponApplicationResult{FinalAmount: order.PriceAmount}
	switch order.Status {
	case "pending":
		couponResult, err = s.applyCoupon(ctx, req.CouponCode, order)
		if err != nil {
			return nil, err
		}
		if couponResult.Applied {
			payableOrder.PriceAmount = couponResult.FinalAmount
		}
		// 2. Create invoice
		invoiceID, err = s.orchestrator.CreateInvoiceForPayment(payableOrder)
		if err != nil {
			log.Printf("[PaymentAppService] WARNING: invoice creation failed for order %s: %v", req.OrderID, err)
			// Non-fatal — continue without invoice
		} else {
			order.InvoiceID = invoiceID
			payableOrder.InvoiceID = invoiceID
		}
		if payableOrder.PriceAmount == 0 {
			if err := s.orchestrator.HandlePaymentConfirmed(req.OrderID); err != nil {
				return nil, apperr.ErrUnprocessable(apperr.CodePaymentFailed, "free order confirmation failed: "+err.Error())
			}
			return &InitiatePaymentResponse{
				OrderID:        req.OrderID,
				InvoiceID:      invoiceID,
				ChargeID:       "coupon:" + couponResult.CouponID,
				Status:         domain.ChargeStatusSuccess,
				PaymentURL:     "",
				Message:        "coupon redeemed, order activated",
				PayableAmount:  payableOrder.PriceAmount,
				DiscountAmount: couponResult.DiscountAmount,
				CouponID:       couponResult.CouponID,
			}, nil
		}
	case "active", "suspended":
		if req.CouponCode != "" {
			return nil, apperr.ErrBadRequest(apperr.CodeCouponInvalid, "coupon_code is only supported for first payment")
		}
		if s.renewals == nil {
			return nil, apperr.ErrUnprocessable(apperr.CodeOrderNotPending,
				"order is not in pending status, current: "+order.Status)
		}
		invoiceID, err = s.renewals.PreparePayment(order)
		if err != nil {
			return nil, apperr.ErrUnprocessable(apperr.CodeOrderNotPending, err.Error())
		}
	default:
		return nil, apperr.ErrUnprocessable(apperr.CodeOrderNotPending,
			"order is not payable in current status: "+order.Status)
	}

	// 3. Create charge — route based on provider_id or legacy flow
	chargeResult, err := s.createCharge(ctx, req, payableOrder)
	if err != nil {
		// Void orphan invoice on charge failure
		s.orchestrator.VoidInvoiceOnFailure(order, invoiceID, "payment charge creation failed: "+err.Error())
		return nil, err // already an *AppError from createCharge
	}

	log.Printf("[PaymentAppService] charge created: order=%s charge=%s invoice=%s status=%s network=%s",
		req.OrderID, chargeResult.ChargeID, invoiceID, chargeResult.Status, req.Network)

	// 4. Schedule invoice timeout
	s.orchestrator.ScheduleInvoiceTimeout(invoiceID, req.OrderID, 30*time.Minute)

	// 5. Build response
	return &InitiatePaymentResponse{
		OrderID:        req.OrderID,
		InvoiceID:      invoiceID,
		ChargeID:       chargeResult.ChargeID,
		Status:         chargeResult.Status,
		PaymentURL:     chargeResult.PaymentURL,
		Message:        "payment initiated, awaiting confirmation",
		PayableAmount:  payableOrder.PriceAmount,
		DiscountAmount: couponResult.DiscountAmount,
		CouponID:       couponResult.CouponID,
		Crypto:         chargeResult.Crypto,
	}, nil
}

func (s *PaymentAppService) applyCoupon(ctx context.Context, code string, order PayableOrder) (CouponApplicationResult, error) {
	if s.coupons == nil {
		if code != "" {
			return CouponApplicationResult{}, apperr.ErrUnprocessable(apperr.CodeCouponInvalid, "coupon service not configured")
		}
		return CouponApplicationResult{FinalAmount: order.PriceAmount}, nil
	}

	result, err := s.coupons.ApplyCoupon(ctx, CouponApplicationRequest{
		Code:           code,
		UserID:         order.CustomerID,
		OrderID:        order.ID,
		ProductID:      order.ProductID,
		OriginalAmount: order.PriceAmount,
	})
	if err != nil {
		return CouponApplicationResult{}, err
	}
	if !result.Applied {
		result.FinalAmount = order.PriceAmount
	}
	return result, nil
}

// createCharge routes the charge creation to the correct provider.
// Returns *AppError for all known business errors.
func (s *PaymentAppService) createCharge(ctx context.Context, req *InitiatePaymentRequest, order PayableOrder) (*domain.ChargeResult, error) {
	// ── Dynamic provider routing (provider_id specified) ──
	if req.ProviderID != "" {
		return s.chargeViaDynamicProvider(ctx, req, order)
	}

	// ── Legacy flow: crypto provider with optional network ──
	if s.cryptoProv != nil {
		return s.chargeViaCrypto(ctx, req, order)
	}

	// No provider available
	return nil, apperr.ErrUnprocessable(apperr.CodePaymentFailed, "no payment provider configured")
}

// chargeViaDynamicProvider routes to a dynamically configured provider.
func (s *PaymentAppService) chargeViaDynamicProvider(ctx context.Context, req *InitiatePaymentRequest, order PayableOrder) (*domain.ChargeResult, error) {
	if s.providerSvc == nil {
		return nil, apperr.ErrInternal("provider service not configured")
	}

	providerCfg, err := s.providerSvc.GetProviderConfig(req.ProviderID)
	if err != nil {
		return nil, apperr.ErrNotFound(apperr.CodeProviderNotFound, "payment provider not found")
	}
	if !providerCfg.Enabled {
		return nil, apperr.ErrUnprocessable(apperr.CodeInvalidParams, "payment provider is disabled")
	}

	switch providerCfg.Type {
	case domain.ProviderTypeCryptoUSDT:
		// Crypto USDT — delegate to crypto provider with optional network
		if s.cryptoProv == nil {
			return nil, apperr.ErrUnprocessable(apperr.CodeCryptoNotConfigured, "crypto payments not configured")
		}
		network := domain.CryptoNetwork(req.Network)
		if req.Network == "" {
			network = domain.NetworkArbitrum // default
		} else if !domain.ValidNetwork(req.Network) {
			return nil, apperr.ErrBadRequest(apperr.CodeNetworkUnsupported,
				fmt.Sprintf("unsupported network: %s (supported: arbitrum, solana, trc20, bsc, polygon)", req.Network))
		}
		result, err := s.cryptoProv.CreateCryptoCharge(ctx, order.ID, order.PriceAmount, network)
		if err != nil {
			return nil, apperr.ErrUnprocessable(apperr.CodePaymentFailed, "crypto payment failed: "+err.Error())
		}
		return result, nil
	case domain.ProviderTypeEPay:
		payType := strings.TrimSpace(req.PayType)
		if payType == "" {
			return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "pay_type is required for EPay")
		}
		prov, err := s.providerSvc.GetProviderWithConfigOverride(providerCfg.ID, map[string]interface{}{
			"pay_type": payType,
		})
		if err != nil {
			return nil, providerRuntimeError(err)
		}
		res, err := prov.CreateCharge(ctx, order.ID, order.Currency, order.PriceAmount)
		if err != nil {
			return nil, err
		}
		return res, nil
	default:
		// All other types — use the factory-based provider resolution
		provider, err := s.providerSvc.GetProvider(req.ProviderID)
		if err != nil {
			return nil, apperr.ErrUnprocessable(apperr.CodeInternalError,
				fmt.Sprintf("provider type %q: %v", providerCfg.Type, err))
		}
		result, err := provider.CreateCharge(ctx, order.ID, order.Currency, order.PriceAmount)
		if err != nil {
			return nil, apperr.ErrUnprocessable(apperr.CodePaymentFailed, "payment failed: "+err.Error())
		}
		return result, nil
	}
}

// chargeViaCrypto handles the legacy crypto payment flow (no provider_id).
func (s *PaymentAppService) chargeViaCrypto(ctx context.Context, req *InitiatePaymentRequest, order PayableOrder) (*domain.ChargeResult, error) {
	network := domain.NetworkArbitrum // default
	if req.Network != "" {
		if !domain.ValidNetwork(req.Network) {
			return nil, apperr.ErrBadRequest(apperr.CodeNetworkUnsupported,
				fmt.Sprintf("unsupported network: %s (supported: arbitrum, solana, trc20, bsc, polygon)", req.Network))
		}
		network = domain.CryptoNetwork(req.Network)
	}

	result, err := s.cryptoProv.CreateCryptoCharge(ctx, order.ID, order.PriceAmount, network)
	if err != nil {
		return nil, apperr.ErrUnprocessable(apperr.CodePaymentFailed, "crypto payment failed: "+err.Error())
	}
	return result, nil
}

// ── Webhook handling ───────────────────────────────────────────────────

// HandleWebhookPayload processes a webhook payload from any payment provider.
// Called by the crypto provider's async callback or EPay webhook verification.
func (s *PaymentAppService) HandleWebhookPayload(payload *domain.WebhookPayload) {
	log.Printf("[PaymentAppService.HandleWebhookPayload] processing: charge=%s order=%s status=%s",
		payload.ChargeID, payload.OrderID, payload.Status)
	defer s.publishPaymentStatus(PaymentStatusEvent{
		OrderID:  payload.OrderID,
		ChargeID: payload.ChargeID,
		Status:   payload.Status,
		Message:  "payment webhook processed",
	})

	if payload.Status != domain.ChargeStatusSuccess {
		if payload.Status == domain.ChargeStatusFailed {
			order, err := s.orchestrator.GetOrderForPay(payload.OrderID)
			if err != nil {
				log.Printf("[PaymentAppService.HandleWebhookPayload] WARNING: order lookup failed for failed payment: %v", err)
				return
			}
			s.orchestrator.VoidInvoiceOnFailure(order, order.InvoiceID,
				"payment gateway reported unsuccessful payment")
		}
		log.Printf("[PaymentAppService.HandleWebhookPayload] payment not successful (status=%s), skipping activation",
			payload.Status)
		return
	}

	order, err := s.orchestrator.GetOrderForPay(payload.OrderID)
	if err != nil {
		log.Printf("[PaymentAppService.HandleWebhookPayload] WARNING: order lookup failed: %v", err)
		return
	}

	switch order.Status {
	case "pending":
		if err := s.orchestrator.HandlePaymentConfirmed(payload.OrderID); err != nil {
			log.Printf("[PaymentAppService.HandleWebhookPayload] WARNING: post-payment flow failed: %v", err)
		}
	case "active", "suspended":
		if s.renewals == nil {
			log.Printf("[PaymentAppService.HandleWebhookPayload] WARNING: renewal service not configured for order=%s", payload.OrderID)
			return
		}
		if err := s.renewals.HandlePaidOrder(order); err != nil {
			log.Printf("[PaymentAppService.HandleWebhookPayload] WARNING: renewal payment flow failed: %v", err)
		}
	default:
		log.Printf("[PaymentAppService.HandleWebhookPayload] ignoring successful payment for order=%s status=%s", payload.OrderID, order.Status)
	}
}

func (s *PaymentAppService) SubscribePaymentStatus(orderID string) (<-chan PaymentStatusEvent, func()) {
	ch := make(chan PaymentStatusEvent, 4)
	s.statusMu.Lock()
	if s.statusSubscribers[orderID] == nil {
		s.statusSubscribers[orderID] = make(map[chan PaymentStatusEvent]struct{})
	}
	s.statusSubscribers[orderID][ch] = struct{}{}
	s.statusMu.Unlock()

	unsubscribe := func() {
		s.statusMu.Lock()
		if subscribers := s.statusSubscribers[orderID]; subscribers != nil {
			if _, ok := subscribers[ch]; ok {
				delete(subscribers, ch)
				close(ch)
			}
			if len(subscribers) == 0 {
				delete(s.statusSubscribers, orderID)
			}
		}
		s.statusMu.Unlock()
	}
	return ch, unsubscribe
}

func (s *PaymentAppService) publishPaymentStatus(event PaymentStatusEvent) {
	if event.OrderID == "" {
		return
	}
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	for ch := range s.statusSubscribers[event.OrderID] {
		select {
		case ch <- event:
		default:
		}
	}
}

// VerifyProviderWebhook looks up a provider by ID and verifies a webhook payload.
// Used for provider-specific webhook endpoints (e.g. EPay).
func (s *PaymentAppService) VerifyProviderWebhook(providerID string, rawBody []byte, headers domain.WebhookHeaders) (*domain.WebhookPayload, error) {
	if s.providerSvc == nil {
		return nil, apperr.ErrInternal("provider service not configured")
	}

	providerCfg, err := s.providerSvc.GetProviderConfig(providerID)
	if err != nil {
		return nil, apperr.ErrNotFound(apperr.CodeProviderNotFound, "provider not found")
	}
	if !providerCfg.Enabled {
		return nil, apperr.ErrUnprocessable(apperr.CodeInvalidParams, "provider is disabled")
	}

	provider, err := s.providerSvc.GetProvider(providerID)
	if err != nil {
		return nil, providerRuntimeError(err)
	}

	payload, err := provider.VerifyWebhook(rawBody, headers)
	if err != nil {
		return nil, apperr.ErrBadRequest(apperr.CodeWebhookFailed, "webhook verification failed: "+err.Error())
	}
	return payload, nil
}

// HandleProviderReturn validates a gateway browser return and reconciles it
// through the same internal confirmation path used by webhooks. Return URLs are
// not the primary delivery mechanism, but they provide a useful fast path when
// the customer lands back before the async notify_url arrives.
func (s *PaymentAppService) HandleProviderReturn(providerID string, rawQuery []byte) (*ProviderReturnResult, error) {
	if len(rawQuery) == 0 {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "empty payment return query")
	}

	payload, err := s.VerifyProviderWebhook(providerID, rawQuery, nil)
	if err != nil {
		return nil, err
	}
	if payload.OrderID == "" {
		return nil, apperr.ErrBadRequest(apperr.CodeWebhookFailed, "payment return missing order id")
	}

	order, err := s.orchestrator.GetOrderForPay(payload.OrderID)
	if err != nil {
		return nil, apperr.ErrNotFound(apperr.CodeOrderNotFound, "order not found: "+err.Error())
	}
	if err := s.validateEPayReturn(providerID, rawQuery, order); err != nil {
		return nil, err
	}

	if payload.Status == domain.ChargeStatusSuccess {
		s.HandleWebhookPayload(payload)
	} else {
		s.publishPaymentStatus(PaymentStatusEvent{
			OrderID:  payload.OrderID,
			ChargeID: payload.ChargeID,
			Status:   payload.Status,
			Message:  "payment return verified",
		})
	}

	return &ProviderReturnResult{
		OrderID: payload.OrderID,
		Status:  payload.Status,
	}, nil
}

func (s *PaymentAppService) validateEPayReturn(providerID string, rawQuery []byte, order PayableOrder) error {
	values, err := url.ParseQuery(string(rawQuery))
	if err != nil {
		return apperr.ErrBadRequest(apperr.CodeInvalidParams, "invalid payment return query: "+err.Error())
	}

	providerCfg, err := s.providerSvc.GetProviderConfig(providerID)
	if err != nil {
		return apperr.ErrNotFound(apperr.CodeProviderNotFound, "provider not found")
	}
	if pid, _ := providerCfg.Config["pid"].(string); strings.TrimSpace(pid) != "" {
		if got := strings.TrimSpace(values.Get("pid")); got != strings.TrimSpace(pid) {
			return apperr.ErrBadRequest(apperr.CodeWebhookFailed, "payment return pid mismatch")
		}
	}

	outTradeNo := strings.TrimSpace(values.Get("out_trade_no"))
	if outTradeNo != "" && outTradeNo != order.ID {
		return apperr.ErrBadRequest(apperr.CodeWebhookFailed, "payment return order mismatch")
	}

	if money := strings.TrimSpace(values.Get("money")); money != "" {
		expectedAmount := order.PriceAmount
		if order.InvoiceID != "" {
			if invoice, err := s.orchestrator.GetInvoiceForPayment(order.InvoiceID); err == nil {
				expectedAmount = invoice.Total
			}
		}
		expected := fmt.Sprintf("%.2f", float64(expectedAmount)/100.0)
		if money != expected {
			return apperr.ErrBadRequest(apperr.CodeWebhookFailed, "payment return amount mismatch")
		}
	}

	return nil
}

func providerRuntimeError(err error) error {
	if errors.Is(err, domain.ErrPublicBaseURLRequired) {
		return apperr.ErrUnprocessable(apperr.CodeInvalidParams, err.Error())
	}
	return apperr.ErrInternal("failed to construct provider: " + err.Error())
}

// GetCryptoNetworks returns supported blockchain networks.
func (s *PaymentAppService) GetCryptoNetworks() []domain.NetworkInfo {
	if s.cryptoProv == nil {
		return nil
	}
	return s.cryptoProv.GetNetworks()
}

// GetCryptoChargeDetail returns the crypto-specific details for a charge.
func (s *PaymentAppService) GetCryptoChargeDetail(chargeID string) *domain.CryptoChargeDetail {
	if s.cryptoProv == nil {
		return nil
	}
	return s.cryptoProv.GetChargeDetail(chargeID)
}

// GetDefaultProvider returns the default payment provider for direct webhook
// verification (legacy flow). May be nil if no crypto provider is configured.
func (s *PaymentAppService) GetDefaultProvider() domain.PaymentProvider {
	if s.cryptoProv != nil {
		return s.cryptoProv
	}
	return nil
}
