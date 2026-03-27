package app

import (
	"backend-core/internal/payment/domain"
	"fmt"
	"log"
)

type PaymentType string

const (
	CryptoPayment PaymentType = "crypto"
	AliPay        PaymentType = "alipay"
	Wechat        PaymentType = "wechat"
	EPay          PaymentType = "epay"
)

// PaymentAppService implements domain.CheckoutProcessor.
// It delegates the actual charge to whatever PaymentProvider is injected.
type PaymentAppService struct {
	providerSvc *ProviderAppService
}

func NewPaymentAppService(provider *ProviderAppService) *PaymentAppService {
	return &PaymentAppService{providerSvc: provider}
}

// Process creates a charge through the configured payment provider.
func (s *PaymentAppService) Process(orderID string, currency string, amountMinor int64, provider domain.PaymentProvider) (*domain.ChargeResult, error) {
	if orderID == "" {
		return nil, fmt.Errorf("app_error: order id is required")
	}
	if amountMinor <= 0 {
		return nil, fmt.Errorf("app_error: amount must be > 0")
	}

	result, err := provider.CreateCharge(orderID, currency, amountMinor)
	if err != nil {
		return nil, fmt.Errorf("app_error: payment failed: %w", err)
	}

	log.Printf("[PaymentAppService] charge processed: order=%s charge=%s status=%s", orderID, result.ChargeID, result.Status)
	return result, nil
}

// app/payment_app.go
func (s *PaymentAppService) InitiatePayment(req *domain.PaymentRequest, providerID string) (*domain.ChargeResult, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validation_error: %w", err)
	}

	// 2. 获取 provider
	provider, err := s.providerSvc.GetProvider(providerID)
	if err != nil {
		return nil, fmt.Errorf("provider_error: %w", err)
	}

	switch req.PaymentType {
	case domain.PaymentTypeCrypto:
		cryptoProvider, ok := provider.(domain.CryptoPaymentProvider)
		if !ok {
			return nil, fmt.Errorf("provider does not support crypto payments")
		}
		return cryptoProvider.CreateCryptoCharge(req.OrderID, req.AmountMinor, req.Network)
	default:
		return provider.CreateCharge(req.OrderID, req.Currency, req.AmountMinor)
	}
}
