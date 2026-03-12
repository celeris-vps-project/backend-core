// Package app provides the application service for the unified checkout module.
//
// CheckoutAppService is a thin orchestration layer that delegates real business
// logic to the product and ordering domains. It is consumed by both the
// SyncCheckoutProcessor and AsyncCheckoutProcessor via the adaptive dispatcher.
package app

import (
	productApp "backend-core/internal/catalog/app"
	"backend-core/internal/checkout/domain"
	orderingApp "backend-core/internal/ordering/app"
	"fmt"
	"log"
)

// CheckoutAppService orchestrates the cross-domain checkout flow:
//
//  1. Validate the product (enabled? has available slots?)
//  2. Call ordering.CreateOrder() → persist the order in pending status
//
// Slot consumption (PurchaseProduct) and provisioning happen ONLY after
// payment confirmation, in the payment webhook handler. This ensures
// inventory is never burned until the customer actually pays.
//
// This service is called by both the sync and async checkout processors.
// It does NOT decide sync vs. async — that's the adaptive dispatcher's job.
type CheckoutAppService struct {
	productSvc  *productApp.ProductAppService
	orderingSvc *orderingApp.OrderAppService
}

// NewCheckoutAppService creates a checkout orchestration service.
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
// Both the sync processor (directly) and async processor (via background worker)
// call this method.
//
// Returns a CheckoutResult with HTTPStatus=200 on success.
func (s *CheckoutAppService) Execute(req domain.CheckoutRequest) (*domain.CheckoutResult, error) {
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
	product, err := s.productSvc.GetProduct(req.ProductID)
	if err != nil {
		return nil, fmt.Errorf("checkout_error: product not found: %w", err)
	}
	if !product.Enabled() {
		return nil, fmt.Errorf("checkout_error: product %s is not available", req.ProductID)
	}

	// 2. Create the order first (in pending status)
	order, err := s.orderingSvc.CreateOrder(
		req.CustomerID,
		req.ProductID,
		"", // invoiceID — will be linked after payment
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
