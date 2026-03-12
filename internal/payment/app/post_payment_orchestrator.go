package app

import (
	"fmt"
	"log"
)

// PostPaymentOrchestrator orchestrates the cross-domain flow after a
// payment is confirmed via webhook:
//  1. Activate the order (ordering context)
//  2. Consume product slot + trigger provisioning (catalog context)
//  3. Create a pending instance (instance context)
//
// This keeps the PaymentHandler thin (only calls payment app services)
// and centralizes cross-domain logic in the app layer where it belongs.

// OrderActivator is a port for the ordering context.
type OrderActivator interface {
	ActivateOrder(orderID string) error
	GetOrderForPayment(orderID string) (PayableOrder, error)
}

// PayableOrder is the minimal read-model the payment context needs.
type PayableOrder struct {
	ID          string
	CustomerID  string
	ProductID   string
	Status      string
	Currency    string
	PriceAmount int64
	Hostname    string
	Plan        string
	Region      string
	OS          string
	CPU         int
	MemoryMB    int
	DiskGB      int
}

// ProductPurchaser is a port for the catalog context.
type ProductPurchaser interface {
	PurchaseProduct(productID, customerID, orderID, hostname, os string) (PurchasedProduct, error)
}

// PurchasedProduct is the minimal read-model returned after purchase.
type PurchasedProduct struct {
	Location string
	Slug     string
	CPU      int
	MemoryMB int
	DiskGB   int
}

// InstanceCreator is a port for the instance context.
// Returns the newly created instance ID or an error.
type InstanceCreator interface {
	CreatePendingInstance(customerID, orderID, region, hostname, plan, os string, cpu, memoryMB, diskGB int) (string, error)
}

// PostPaymentOrchestrator wires together the ordering, catalog, and instance
// contexts after a successful payment confirmation.
type PostPaymentOrchestrator struct {
	orders    OrderActivator
	products  ProductPurchaser
	instances InstanceCreator // nil = skip instance creation
}

// NewPostPaymentOrchestrator builds the orchestrator with the three cross-domain ports.
func NewPostPaymentOrchestrator(o OrderActivator, p ProductPurchaser, i InstanceCreator) *PostPaymentOrchestrator {
	return &PostPaymentOrchestrator{orders: o, products: p, instances: i}
}

// HandlePaymentConfirmed runs the full post-payment flow for a confirmed order:
//  1. ActivateOrder   — transitions order pending → active
//  2. GetOrder        — reloads order to obtain VPS configuration
//  3. PurchaseProduct — consumes a commercial slot & fires provisioning event
//  4. CreatePendingInstance — creates an instance record immediately visible to the user
//
// A non-nil error is returned only for critical failures; instance-creation
// failures are logged but do not roll back the order activation.
func (s *PostPaymentOrchestrator) HandlePaymentConfirmed(orderID string) error {
	// 1. Activate the order (pending → active)
	if err := s.orders.ActivateOrder(orderID); err != nil {
		return fmt.Errorf("activate order: %w", err)
	}

	// 2. Reload the order to obtain VPS configuration for downstream steps
	order, err := s.orders.GetOrderForPayment(orderID)
	if err != nil {
		return fmt.Errorf("get order after activation: %w", err)
	}

	// 3. Consume a commercial slot and publish the ProductPurchasedEvent
	product, err := s.products.PurchaseProduct(
		order.ProductID,
		order.CustomerID,
		orderID,
		order.Hostname,
		order.OS,
	)
	if err != nil {
		// Order is already activated — provisioning can be retried.
		log.Printf("[PostPaymentOrchestrator] WARNING: product purchase failed for order %s: %v", orderID, err)
		return fmt.Errorf("purchase product: %w", err)
	}

	// 4. Create a pending instance — immediately visible to the user.
	//    The provisioning bus will handle async provisioning.
	if s.instances != nil {
		instanceID, err := s.instances.CreatePendingInstance(
			order.CustomerID,
			orderID,
			product.Location,
			order.Hostname,
			product.Slug,
			order.OS,
			product.CPU,
			product.MemoryMB,
			product.DiskGB,
		)
		if err != nil {
			// Instance creation failure is non-fatal; provisioning can be retried.
			log.Printf("[PostPaymentOrchestrator] WARNING: create pending instance failed for order %s: %v", orderID, err)
		} else {
			log.Printf("[PostPaymentOrchestrator] pending instance created: %s (order=%s)", instanceID, orderID)
		}
	}

	log.Printf("[PostPaymentOrchestrator] post-payment flow complete: order=%s → activated → provisioned", orderID)
	return nil
}

// GetOrderForPay retrieves an order as a PayableOrder DTO for use in the
// payment initiation flow (Pay handler). This avoids importing ordering types
// into the interface layer.
func (s *PostPaymentOrchestrator) GetOrderForPay(orderID string) (PayableOrder, error) {
	return s.orders.GetOrderForPayment(orderID)
}
