package app

import (
	"backend-core/internal/payment/domain"
	"fmt"
	"log"
)

// PaymentAppService implements domain.CheckoutProcessor.
// It delegates the actual charge to whatever PaymentProvider is injected.
type PaymentAppService struct {
	provider domain.PaymentProvider
}

func NewPaymentAppService(provider domain.PaymentProvider) *PaymentAppService {
	return &PaymentAppService{provider: provider}
}

// Process creates a charge through the configured payment provider.
func (s *PaymentAppService) Process(orderID string, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	if orderID == "" {
		return nil, fmt.Errorf("app_error: order id is required")
	}
	if amountMinor <= 0 {
		return nil, fmt.Errorf("app_error: amount must be > 0")
	}

	result, err := s.provider.CreateCharge(orderID, currency, amountMinor)
	if err != nil {
		return nil, fmt.Errorf("app_error: payment failed: %w", err)
	}

	log.Printf("[PaymentAppService] charge processed: order=%s charge=%s status=%s", orderID, result.ChargeID, result.Status)
	return result, nil
}
