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
	CustomerID string // optional — when set, order ownership is enforced
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

type ReconcilePaymentResult struct {
	OrderID    string
	ProviderID string
	Status     string
	Updated    bool
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
	attempts     domain.PaymentAttemptRepo
	ids          IDGen

	statusMu          sync.RWMutex
	statusSubscribers map[string]map[chan PaymentStatusEvent]struct{}

	initiationMu    sync.Mutex
	initiationLocks map[string]*paymentInitiationLock
}

type paymentInitiationLock struct {
	mu   sync.Mutex
	refs int
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
		initiationLocks:   make(map[string]*paymentInitiationLock),
	}
}

func (s *PaymentAppService) SetRenewalService(renewals *RenewalService) {
	s.renewals = renewals
}

func (s *PaymentAppService) SetCouponApplier(coupons CouponApplier) {
	s.coupons = coupons
}

func (s *PaymentAppService) SetPaymentAttemptStore(repo domain.PaymentAttemptRepo, ids IDGen) {
	s.attempts = repo
	s.ids = ids
}

func (s *PaymentAppService) StartPendingPaymentPoller(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Printf("[PaymentAppService] pending payment poller started (interval=%v)", interval)
		for {
			select {
			case <-ticker.C:
				s.pollPendingPayments(ctx)
			case <-ctx.Done():
				log.Printf("[PaymentAppService] pending payment poller stopped")
				return
			}
		}
	}()
}

// InitiatePayment is the single entry-point for starting a payment.
// It handles order lookup, invoice creation, provider routing, charge
// creation, and timeout scheduling.
//
// Returns *AppError for all known business errors so the handler can
// call apperr.HandleErr() without any business logic.
func (s *PaymentAppService) InitiatePayment(ctx context.Context, req *InitiatePaymentRequest) (*InitiatePaymentResponse, error) {
	if req == nil || strings.TrimSpace(req.OrderID) == "" {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "order_id is required")
	}
	req.OrderID = strings.TrimSpace(req.OrderID)

	unlock := s.lockPaymentInitiation(req.OrderID)
	defer unlock()

	// 1. Look up the order
	order, err := s.orchestrator.GetOrderForPay(req.OrderID)
	if err != nil {
		return nil, apperr.ErrNotFound(apperr.CodeOrderNotFound, "order not found: "+err.Error())
	}
	req.CustomerID = strings.TrimSpace(req.CustomerID)
	if req.CustomerID != "" && order.CustomerID != req.CustomerID {
		return nil, &apperr.AppError{
			Code:       apperr.CodeForbidden,
			Message:    "order access denied",
			HTTPStatus: 403,
		}
	}

	if resp, ok := s.reusePendingPayment(ctx, order); ok {
		return resp, nil
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

	s.recordPaymentAttempt(ctx, req, payableOrder, chargeResult)

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

func (s *PaymentAppService) lockPaymentInitiation(orderID string) func() {
	s.initiationMu.Lock()
	if s.initiationLocks == nil {
		s.initiationLocks = make(map[string]*paymentInitiationLock)
	}
	lock := s.initiationLocks[orderID]
	if lock == nil {
		lock = &paymentInitiationLock{}
		s.initiationLocks[orderID] = lock
	}
	lock.refs++
	s.initiationMu.Unlock()

	lock.mu.Lock()

	return func() {
		lock.mu.Unlock()

		s.initiationMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.initiationLocks, orderID)
		}
		s.initiationMu.Unlock()
	}
}

func (s *PaymentAppService) reusePendingPayment(ctx context.Context, order PayableOrder) (*InitiatePaymentResponse, bool) {
	if order.Status != "pending" || s.attempts == nil {
		return nil, false
	}
	attempt, err := s.attempts.FindLatestByOrderID(ctx, order.ID)
	if err != nil {
		if !errors.Is(err, domain.ErrPaymentAttemptNotFound) {
			log.Printf("[PaymentAppService] WARNING: payment attempt lookup failed for idempotent pay: order=%s err=%v",
				order.ID, err)
		}
		return nil, false
	}
	if attempt == nil || attempt.Status != domain.ChargeStatusPending || strings.TrimSpace(attempt.PayURL) == "" {
		return nil, false
	}

	payableAmount := order.PriceAmount
	if order.InvoiceID != "" && s.orchestrator != nil {
		invoice, err := s.orchestrator.GetInvoiceForPayment(order.InvoiceID)
		if err != nil {
			log.Printf("[PaymentAppService] WARNING: invoice lookup failed for idempotent pay: order=%s invoice=%s err=%v",
				order.ID, order.InvoiceID, err)
		} else {
			if invoice.Status == "void" {
				return nil, false
			}
			payableAmount = invoice.Total
		}
	}

	log.Printf("[PaymentAppService] reusing pending payment: order=%s attempt=%s provider=%s",
		order.ID, attempt.ID, attempt.ProviderID)
	return &InitiatePaymentResponse{
		OrderID:       order.ID,
		InvoiceID:     order.InvoiceID,
		ChargeID:      pendingAttemptChargeID(attempt),
		Status:        domain.ChargeStatusPending,
		PaymentURL:    strings.TrimSpace(attempt.PayURL),
		Message:       "existing pending payment returned",
		PayableAmount: payableAmount,
	}, true
}

func pendingAttemptChargeID(attempt *domain.PaymentAttempt) string {
	for _, value := range []string{attempt.TradeNo, attempt.OutTradeNo} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return attempt.ID
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

func (s *PaymentAppService) recordPaymentAttempt(ctx context.Context, req *InitiatePaymentRequest, order PayableOrder, result *domain.ChargeResult) {
	if s.attempts == nil || s.ids == nil || req == nil || result == nil {
		return
	}
	providerID := strings.TrimSpace(req.ProviderID)
	if providerID == "" {
		return
	}
	if s.providerSvc != nil {
		cfg, err := s.providerSvc.GetProviderConfig(providerID)
		if err != nil || cfg.Type != domain.ProviderTypeEPay {
			return
		}
	}
	outTradeNo := strings.TrimSpace(result.OutTradeNo)
	if outTradeNo == "" {
		outTradeNo = strings.TrimSpace(order.ID)
	}
	if outTradeNo == "" {
		return
	}

	attempt := &domain.PaymentAttempt{
		ID:         s.ids.NewID(),
		OrderID:    order.ID,
		ProviderID: providerID,
		PayType:    strings.TrimSpace(req.PayType),
		TradeNo:    "",
		OutTradeNo: outTradeNo,
		PayURL:     strings.TrimSpace(result.PaymentURL),
		Status:     result.Status,
	}
	if attempt.Status == "" {
		attempt.Status = domain.ChargeStatusPending
	}
	if err := s.attempts.Create(ctx, attempt); err != nil {
		log.Printf("[PaymentAppService] WARNING: payment attempt create failed: order=%s provider=%s err=%v",
			order.ID, providerID, err)
	}
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
		outTradeNo, err := s.newPaymentAttemptOutTradeNo()
		if err != nil {
			return nil, err
		}
		prov, err := s.providerSvc.GetProviderWithConfigOverride(providerCfg.ID, map[string]interface{}{
			"pay_type": payType,
		})
		if err != nil {
			return nil, providerRuntimeError(err)
		}
		res, err := prov.CreateCharge(ctx, outTradeNo, order.Currency, order.PriceAmount)
		if err != nil {
			return nil, err
		}
		res.OutTradeNo = outTradeNo
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

func (s *PaymentAppService) newPaymentAttemptOutTradeNo() (string, error) {
	if s.ids == nil {
		return "", apperr.ErrInternal("payment attempt id generator not configured")
	}
	return "pay_" + s.ids.NewID(), nil
}

// ── Webhook handling ───────────────────────────────────────────────────

// HandleWebhookPayload processes a webhook payload from any payment provider.
// Called by the crypto provider's async callback or EPay webhook verification.
func (s *PaymentAppService) HandleWebhookPayload(payload *domain.WebhookPayload) {
	log.Printf("[PaymentAppService.HandleWebhookPayload] processing: charge=%s order=%s status=%s",
		payload.ChargeID, payload.OrderID, payload.Status)
	payload = s.normalizePayloadFromAttempt(payload)
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

func (s *PaymentAppService) normalizePayloadFromAttempt(payload *domain.WebhookPayload) *domain.WebhookPayload {
	if s.attempts == nil || payload == nil || strings.TrimSpace(payload.OrderID) == "" {
		return payload
	}
	ctx := context.Background()
	attempt, err := s.attempts.FindByOutTradeNo(ctx, payload.OrderID)
	if err != nil {
		if !errors.Is(err, domain.ErrPaymentAttemptNotFound) {
			log.Printf("[PaymentAppService] WARNING: payment attempt lookup failed: out_trade_no=%s err=%v", payload.OrderID, err)
		}
		return payload
	}
	if status := strings.TrimSpace(payload.Status); status != "" {
		attempt.Status = status
	}
	if tradeNo := strings.TrimSpace(payload.ChargeID); tradeNo != "" {
		attempt.TradeNo = tradeNo
	}
	if err := s.attempts.Update(ctx, attempt); err != nil {
		log.Printf("[PaymentAppService] WARNING: payment attempt update failed: id=%s err=%v", attempt.ID, err)
	}
	normalized := *payload
	normalized.OrderID = attempt.OrderID
	return &normalized
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

func (s *PaymentAppService) GetLocalTerminalPaymentStatus(orderID string) (*PaymentStatusEvent, error) {
	order, err := s.orchestrator.GetOrderForPay(orderID)
	if err != nil {
		return nil, err
	}
	switch order.Status {
	case "active":
		return &PaymentStatusEvent{
			OrderID: order.ID,
			Status:  domain.ChargeStatusSuccess,
			Message: "order status is active",
		}, nil
	case "cancelled", "terminated":
		return &PaymentStatusEvent{
			OrderID: order.ID,
			Status:  domain.ChargeStatusFailed,
			Message: "order status is " + order.Status,
		}, nil
	default:
		return nil, nil
	}
}

func (s *PaymentAppService) CanAccessOrder(orderID, customerID string) error {
	orderID = strings.TrimSpace(orderID)
	customerID = strings.TrimSpace(customerID)
	if orderID == "" || customerID == "" {
		return apperr.ErrBadRequest(apperr.CodeInvalidParams, "order_id and customer_id are required")
	}
	order, err := s.orchestrator.GetOrderForPay(orderID)
	if err != nil {
		return apperr.ErrNotFound(apperr.CodeOrderNotFound, "order not found: "+err.Error())
	}
	if order.CustomerID != customerID {
		return &apperr.AppError{
			Code:       apperr.CodeForbidden,
			Message:    "order access denied",
			HTTPStatus: 403,
		}
	}
	return nil
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

func (s *PaymentAppService) ReconcileEPayOrder(ctx context.Context, providerID, orderID string) (*ReconcilePaymentResult, error) {
	return s.reconcileEPayOrder(ctx, providerID, orderID, true)
}

func (s *PaymentAppService) reconcileEPayOrder(ctx context.Context, providerID, orderID string, failOnNoProvider bool) (*ReconcilePaymentResult, error) {
	if s.providerSvc == nil {
		return nil, apperr.ErrInternal("provider service not configured")
	}
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "order id is required")
	}

	order, err := s.orchestrator.GetOrderForPay(orderID)
	if err != nil {
		return nil, apperr.ErrNotFound(apperr.CodeOrderNotFound, "order not found: "+err.Error())
	}
	if order.Status != "pending" {
		return &ReconcilePaymentResult{OrderID: order.ID, ProviderID: providerID, Status: statusFromOrder(order), Updated: false}, nil
	}
	if s.attempts != nil {
		attempt, err := s.attempts.FindLatestByOrderID(ctx, order.ID)
		if err != nil && !errors.Is(err, domain.ErrPaymentAttemptNotFound) {
			log.Printf("[PaymentAppService] WARNING: payment attempt lookup failed: order=%s err=%v", order.ID, err)
		}
		if err == nil && attempt.ProviderID != "" && (providerID == "" || providerID == attempt.ProviderID) {
			return s.reconcileEPayAttempt(ctx, attempt, order, failOnNoProvider)
		}
	}

	providerIDs, err := s.epayProviderIDs(providerID)
	if err != nil {
		return nil, err
	}
	if len(providerIDs) == 0 {
		if failOnNoProvider {
			return nil, apperr.ErrNotFound(apperr.CodeProviderNotFound, "enabled EPay provider not found")
		}
		return nil, nil
	}

	var lastErr error
	for _, id := range providerIDs {
		result, err := s.queryEPayOrder(ctx, id, order, order.ID)
		if err != nil {
			if errors.Is(err, domain.ErrPaymentOrderNotFound) {
				lastErr = err
				continue
			}
			lastErr = err
			log.Printf("[PaymentAppService] EPay query failed: provider=%s order=%s err=%v", id, order.ID, err)
			continue
		}
		updated := false
		if result.Status == domain.ChargeStatusSuccess {
			payload := result.WebhookPayload()
			if payload == nil {
				return nil, apperr.ErrInternal("empty EPay query result")
			}
			s.HandleWebhookPayload(payload)
			updated = true
		} else {
			s.publishPaymentStatus(PaymentStatusEvent{
				OrderID:  result.OrderID,
				ChargeID: result.ChargeID,
				Status:   result.Status,
				Message:  "payment order query processed",
			})
		}
		return &ReconcilePaymentResult{
			OrderID:    result.OrderID,
			ProviderID: id,
			Status:     result.Status,
			Updated:    updated,
		}, nil
	}
	if lastErr != nil {
		if errors.Is(lastErr, domain.ErrPaymentOrderNotFound) && !failOnNoProvider {
			return nil, nil
		}
		return nil, lastErr
	}
	return nil, nil
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
	payload = s.normalizePayloadFromAttempt(payload)

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

func (s *PaymentAppService) ResolvePaymentAttemptOrderID(providerID, outTradeNo string) (string, error) {
	if s.attempts == nil {
		return "", domain.ErrPaymentAttemptNotFound
	}
	outTradeNo = strings.TrimSpace(outTradeNo)
	if outTradeNo == "" {
		return "", domain.ErrPaymentAttemptNotFound
	}
	attempt, err := s.attempts.FindByOutTradeNo(context.Background(), outTradeNo)
	if err != nil {
		return "", err
	}
	if providerID = strings.TrimSpace(providerID); providerID != "" && attempt.ProviderID != providerID {
		return "", domain.ErrPaymentAttemptNotFound
	}
	return attempt.OrderID, nil
}

func (s *PaymentAppService) pollPendingPayments(ctx context.Context) {
	if s.orchestrator == nil {
		return
	}
	if s.attempts != nil {
		attempts, err := s.attempts.ListPending(ctx, 100)
		if err != nil {
			log.Printf("[PaymentAppService] pending payment poller list attempts failed: %v", err)
			return
		}
		for _, attempt := range attempts {
			if attempt == nil || strings.TrimSpace(attempt.OrderID) == "" || strings.TrimSpace(attempt.ProviderID) == "" {
				continue
			}
			order, err := s.orchestrator.GetOrderForPay(attempt.OrderID)
			if err != nil {
				log.Printf("[PaymentAppService] pending payment poller order lookup failed: order=%s err=%v", attempt.OrderID, err)
				continue
			}
			if order.Status != "pending" {
				attempt.Status = statusFromOrder(order)
				if updateErr := s.attempts.Update(ctx, attempt); updateErr != nil {
					log.Printf("[PaymentAppService] pending payment poller attempt status update failed: id=%s err=%v", attempt.ID, updateErr)
				}
				continue
			}
			if _, err := s.reconcileEPayAttempt(ctx, attempt, order, false); err != nil {
				log.Printf("[PaymentAppService] pending payment poller reconcile failed: order=%s provider=%s err=%v",
					attempt.OrderID, attempt.ProviderID, err)
			}
		}
		return
	}
	orders, err := s.orchestrator.ListOrdersForPay()
	if err != nil {
		log.Printf("[PaymentAppService] pending payment poller list orders failed: %v", err)
		return
	}
	for _, order := range orders {
		if order.Status != "pending" || order.InvoiceID == "" {
			continue
		}
		if _, err := s.reconcileEPayOrder(ctx, "", order.ID, false); err != nil {
			log.Printf("[PaymentAppService] pending payment poller reconcile failed: order=%s err=%v", order.ID, err)
		}
	}
}

func (s *PaymentAppService) reconcileEPayAttempt(ctx context.Context, attempt *domain.PaymentAttempt, order PayableOrder, failOnNoProvider bool) (*ReconcilePaymentResult, error) {
	if attempt == nil {
		return nil, nil
	}
	if strings.TrimSpace(attempt.ProviderID) == "" {
		if failOnNoProvider {
			return nil, apperr.ErrNotFound(apperr.CodeProviderNotFound, "payment attempt provider not found")
		}
		return nil, nil
	}
	if _, err := s.epayProviderIDs(attempt.ProviderID); err != nil {
		if failOnNoProvider {
			return nil, err
		}
		return nil, nil
	}
	outTradeNo := strings.TrimSpace(attempt.OutTradeNo)
	if outTradeNo == "" {
		outTradeNo = order.ID
	}
	result, err := s.queryEPayOrder(ctx, attempt.ProviderID, order, outTradeNo)
	if err != nil {
		if errors.Is(err, domain.ErrPaymentOrderNotFound) && !failOnNoProvider {
			return nil, nil
		}
		return nil, err
	}
	updated := false
	if result.Status == domain.ChargeStatusSuccess {
		payload := result.WebhookPayload()
		if payload == nil {
			return nil, apperr.ErrInternal("empty EPay query result")
		}
		s.HandleWebhookPayload(payload)
		updated = true
	} else {
		attempt.Status = result.Status
		if tradeNo := strings.TrimSpace(result.ChargeID); tradeNo != "" {
			attempt.TradeNo = tradeNo
		}
		if err := s.attempts.Update(ctx, attempt); err != nil {
			log.Printf("[PaymentAppService] WARNING: payment attempt update failed: id=%s err=%v", attempt.ID, err)
		}
		s.publishPaymentStatus(PaymentStatusEvent{
			OrderID:  order.ID,
			ChargeID: result.ChargeID,
			Status:   result.Status,
			Message:  "payment order query processed",
		})
	}
	return &ReconcilePaymentResult{
		OrderID:    order.ID,
		ProviderID: attempt.ProviderID,
		Status:     result.Status,
		Updated:    updated,
	}, nil
}

func (s *PaymentAppService) epayProviderIDs(providerID string) ([]string, error) {
	if providerID = strings.TrimSpace(providerID); providerID != "" {
		cfg, err := s.providerSvc.GetProviderConfig(providerID)
		if err != nil {
			return nil, apperr.ErrNotFound(apperr.CodeProviderNotFound, "provider not found")
		}
		if cfg.Type != domain.ProviderTypeEPay {
			return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "provider is not EPay")
		}
		if !cfg.Enabled {
			return nil, apperr.ErrUnprocessable(apperr.CodeInvalidParams, "provider is disabled")
		}
		return []string{cfg.ID}, nil
	}

	providers, err := s.providerSvc.ListEnabledProviders()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(providers))
	for _, cfg := range providers {
		if cfg.Type == domain.ProviderTypeEPay {
			ids = append(ids, cfg.ID)
		}
	}
	return ids, nil
}

func (s *PaymentAppService) queryEPayOrder(ctx context.Context, providerID string, order PayableOrder, outTradeNo string) (*domain.PaymentOrderQueryResult, error) {
	provider, err := s.providerSvc.GetProvider(providerID)
	if err != nil {
		return nil, providerRuntimeError(err)
	}
	querier, ok := provider.(domain.PaymentOrderQuerier)
	if !ok {
		return nil, apperr.ErrUnprocessable(apperr.CodeInvalidParams, "provider does not support order query")
	}
	if outTradeNo = strings.TrimSpace(outTradeNo); outTradeNo == "" {
		outTradeNo = order.ID
	}
	result, err := querier.QueryOrder(ctx, domain.PaymentOrderQuery{OutTradeNo: outTradeNo})
	if err != nil {
		return nil, err
	}
	if err := s.validateEPayQueryResult(providerID, result, order, outTradeNo); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *PaymentAppService) validateEPayQueryResult(providerID string, result *domain.PaymentOrderQueryResult, order PayableOrder, expectedOutTradeNo string) error {
	if result == nil {
		return apperr.ErrBadRequest(apperr.CodeWebhookFailed, "empty payment query result")
	}
	if expectedOutTradeNo = strings.TrimSpace(expectedOutTradeNo); expectedOutTradeNo == "" {
		expectedOutTradeNo = order.ID
	}
	if result.OrderID != "" && result.OrderID != expectedOutTradeNo {
		return apperr.ErrBadRequest(apperr.CodeWebhookFailed, "payment query order mismatch")
	}

	providerCfg, err := s.providerSvc.GetProviderConfig(providerID)
	if err != nil {
		return apperr.ErrNotFound(apperr.CodeProviderNotFound, "provider not found")
	}
	if pid, _ := providerCfg.Config["pid"].(string); strings.TrimSpace(pid) != "" && result.ProviderMerchantID != "" {
		if strings.TrimSpace(result.ProviderMerchantID) != strings.TrimSpace(pid) {
			return apperr.ErrBadRequest(apperr.CodeWebhookFailed, "payment query pid mismatch")
		}
	}

	if result.Amount != "" {
		expectedAmount := order.PriceAmount
		if order.InvoiceID != "" {
			if invoice, err := s.orchestrator.GetInvoiceForPayment(order.InvoiceID); err == nil {
				expectedAmount = invoice.Total
			}
		}
		if !amountMatchesMinor(result.Amount, expectedAmount) {
			return apperr.ErrBadRequest(apperr.CodeWebhookFailed, "payment query amount mismatch")
		}
	}
	return nil
}

func statusFromOrder(order PayableOrder) string {
	switch order.Status {
	case "active":
		return domain.ChargeStatusSuccess
	case "cancelled", "terminated":
		return domain.ChargeStatusFailed
	default:
		return domain.ChargeStatusPending
	}
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

	if money := strings.TrimSpace(values.Get("money")); money != "" {
		expectedAmount := order.PriceAmount
		if order.InvoiceID != "" {
			if invoice, err := s.orchestrator.GetInvoiceForPayment(order.InvoiceID); err == nil {
				expectedAmount = invoice.Total
			}
		}
		if !amountMatchesMinor(money, expectedAmount) {
			return apperr.ErrBadRequest(apperr.CodeWebhookFailed, "payment return amount mismatch")
		}
	}

	return nil
}

func amountMatchesMinor(raw string, expectedMinor int64) bool {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "-") {
		return false
	}
	if strings.HasPrefix(s, "+") {
		s = strings.TrimPrefix(s, "+")
	}
	parts := strings.SplitN(s, ".", 2)
	if len(parts[0]) == 0 {
		parts[0] = "0"
	}
	units, err := parseDigits(parts[0])
	if err != nil {
		return false
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		extra := fraction[2:]
		if strings.Trim(extra, "0") != "" {
			return false
		}
		fraction = fraction[:2]
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	cents, err := parseDigits(fraction)
	if err != nil {
		return false
	}
	return units*100+cents == expectedMinor
}

func parseDigits(s string) (int64, error) {
	var value int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid decimal digit")
		}
		value = value*10 + int64(ch-'0')
	}
	return value, nil
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
