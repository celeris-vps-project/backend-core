package infra

import (
	checkoutApp "backend-core/internal/checkout/app"
	"backend-core/internal/checkout/domain"
)

// SyncCheckoutProcessor processes checkout requests synchronously.
// It delegates to the CheckoutAppService and returns HTTP 200 on success.
//
// This processor is selected by the adaptive dispatcher when QPS < threshold.
type SyncCheckoutProcessor struct {
	svc *checkoutApp.CheckoutAppService
}

// NewSyncCheckoutProcessor creates a synchronous checkout processor.
func NewSyncCheckoutProcessor(svc *checkoutApp.CheckoutAppService) *SyncCheckoutProcessor {
	return &SyncCheckoutProcessor{svc: svc}
}

// Process executes the checkout synchronously and returns HTTP 200.
func (p *SyncCheckoutProcessor) Process(req domain.CheckoutRequest) (*domain.CheckoutResult, error) {
	return p.svc.Execute(req)
}
