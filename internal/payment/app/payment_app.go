package app

import (
	"backend-core/internal/payment/domain"
	"backend-core/pkg/apperr"
	"context"
	"fmt"
	"log"
	"time"
)

// ── Request / Response DTOs ────────────────────────────────────────────

// InitiatePaymentRequest is the app-layer input for starting a payment.
type InitiatePaymentRequest struct {
	OrderID    string // required — the order to pay for
	ProviderID string // optional — dynamic provider selection
	Network    string // optional — crypto network (e.g. "arbitrum")
}

// InitiatePaymentResponse is the app-layer output after initiating a payment.
type InitiatePaymentResponse struct {
	OrderID    string                     `json:"order_id"`
	InvoiceID  string                     `json:"invoice_id,omitempty"`
	ChargeID   string                     `json:"charge_id"`
	Status     string                     `json:"status"`
	PaymentURL string                     `json:"payment_url"`
	Message    string                     `json:"message"`
	Crypto     *domain.CryptoChargeDetail `json:"crypto,omitempty"`
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
}

func NewPaymentAppService(
	providerSvc *ProviderAppService,
	orchestrator *PostPaymentOrchestrator,
	cryptoProv domain.CryptoPaymentProvider,
) *PaymentAppService {
	return &PaymentAppService{
		providerSvc:  providerSvc,
		orchestrator: orchestrator,
		cryptoProv:   cryptoProv,
	}
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
	if order.Status != "pending" {
		return nil, apperr.ErrUnprocessable(apperr.CodeOrderNotPending,
			"order is not in pending status, current: "+order.Status)
	}

	// 2. Create invoice
	invoiceID, err := s.orchestrator.CreateInvoiceForPayment(order)
	if err != nil {
		log.Printf("[PaymentAppService] WARNING: invoice creation failed for order %s: %v", req.OrderID, err)
		// Non-fatal — continue without invoice
	}

	// 3. Create charge — route based on provider_id or legacy flow
	chargeResult, err := s.createCharge(ctx, req, order)
	if err != nil {
		// Void orphan invoice on charge failure
		s.orchestrator.VoidInvoiceOnFailure(invoiceID, "payment charge creation failed: "+err.Error())
		return nil, err // already an *AppError from createCharge
	}

	log.Printf("[PaymentAppService] charge created: order=%s charge=%s invoice=%s status=%s network=%s",
		req.OrderID, chargeResult.ChargeID, invoiceID, chargeResult.Status, req.Network)

	// 4. Schedule invoice timeout
	s.orchestrator.ScheduleInvoiceTimeout(invoiceID, req.OrderID, 30*time.Minute)

	// 5. Build response
	return &InitiatePaymentResponse{
		OrderID:    req.OrderID,
		InvoiceID:  invoiceID,
		ChargeID:   chargeResult.ChargeID,
		Status:     chargeResult.Status,
		PaymentURL: chargeResult.PaymentURL,
		Message:    "payment initiated, awaiting confirmation",
		Crypto:     chargeResult.Crypto,
	}, nil
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
		prov, err := s.providerSvc.GetProvider(providerCfg.ID)
		if err != nil {
			return nil, apperr.ErrNotFound(apperr.CodeProviderNotFound, "unexpected payment provider failed: "+err.Error())
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

	if payload.Status != domain.ChargeStatusSuccess {
		log.Printf("[PaymentAppService.HandleWebhookPayload] payment not successful (status=%s), skipping",
			payload.Status)
		return
	}

	if err := s.orchestrator.HandlePaymentConfirmed(payload.OrderID); err != nil {
		log.Printf("[PaymentAppService.HandleWebhookPayload] WARNING: post-payment flow failed: %v", err)
	}
}

// VerifyProviderWebhook looks up a provider by ID and verifies a webhook payload.
// Used for provider-specific webhook endpoints (e.g. EPay).
func (s *PaymentAppService) VerifyProviderWebhook(providerID string, rawBody []byte, signature string) (*domain.WebhookPayload, error) {
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
		return nil, apperr.ErrInternal("failed to construct provider: " + err.Error())
	}

	payload, err := provider.VerifyWebhook(rawBody, signature)
	if err != nil {
		return nil, apperr.ErrBadRequest(apperr.CodeWebhookFailed, "webhook verification failed: "+err.Error())
	}
	return payload, nil
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
