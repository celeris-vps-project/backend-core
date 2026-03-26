// Package app provides the application service for the unified checkout module.
package app

import (
	productApp "backend-core/internal/catalog/app"
	"backend-core/internal/checkout/domain"
	orderingApp "backend-core/internal/ordering/app"
	"context"
	"fmt"
	"log"
)

// CheckoutAppService orchestrates the cross-domain checkout flow.
type CheckoutAppService struct {
	productSvc  *productApp.ProductAppService
	orderingSvc *orderingApp.OrderAppService
}

func NewCheckoutAppService(
	productSvc *productApp.ProductAppService,
	orderingSvc *orderingApp.OrderAppService,
) *CheckoutAppService {
	return &CheckoutAppService{
		productSvc:  productSvc,
		orderingSvc: orderingSvc,
	}
}

// Execute performs the full checkout flow synchronously.
func (s *CheckoutAppService) Execute(ctx context.Context, req domain.CheckoutRequest) (*domain.CheckoutResult, error) {
	if req.ProductID == "" || req.CustomerID == "" {
		return nil, fmt.Errorf("checkout_error: product_id and customer_id are required")
	}
	if req.Hostname == "" {
		req.Hostname = "vps-" + req.ProductID
	}
	if req.OS == "" {
		req.OS = "ubuntu-22.04"
	}

	// 1. Look up the product to get pricing info
	product, err := s.productSvc.GetProduct(ctx, req.ProductID)
	if err != nil {
		return nil, fmt.Errorf("checkout_error: product not found: %w", err)
	}
	if !product.Enabled() {
		return nil, fmt.Errorf("checkout_error: product %s is not available", req.ProductID)
	}

	// 2. Create the order first (in pending status)
	billingCycle := string(product.BillingCycle())
	order, err := s.orderingSvc.CreateOrder(
		req.CustomerID,
		req.ProductID,
		"",
		billingCycle,
		req.Hostname,
		product.Slug(),
		product.Location(),
		req.OS,
		product.CPU(),
		product.MemoryMB(),
		product.DiskGB(),
		product.Currency(),
		product.PriceAmount(),
	)
	if err != nil {
		return nil, fmt.Errorf("checkout_error: order creation failed: %w", err)
	}

	log.Printf("[CheckoutApp] order created (pending payment): order=%s product=%s customer=%s",
		order.ID(), req.ProductID, req.CustomerID)

	return &domain.CheckoutResult{
		HTTPStatus: 200,
		OrderID:    order.ID(),
		Message:    "order created — awaiting payment confirmation",
	}, nil
}
