package infra

import (
	checkoutApp "backend-core/internal/checkout/app"
	"backend-core/internal/checkout/domain"
	"context"
)

// SyncCheckoutProcessor processes checkout requests synchronously.
type SyncCheckoutProcessor struct {
	svc *checkoutApp.CheckoutAppService
}

func NewSyncCheckoutProcessor(svc *checkoutApp.CheckoutAppService) *SyncCheckoutProcessor {
	return &SyncCheckoutProcessor{svc: svc}
}

func (p *SyncCheckoutProcessor) Process(ctx context.Context, req domain.CheckoutRequest) (*domain.CheckoutResult, error) {
	return p.svc.Execute(ctx, req)
}
