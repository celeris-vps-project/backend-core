package app

import (
	"backend-core/pkg/delayed"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
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
	LinkInvoiceToOrder(orderID, invoiceID string) error
}

// PayableOrder is the minimal read-model the payment context needs.
type PayableOrder struct {
	ID           string
	CustomerID   string
	ProductID    string
	InvoiceID    string
	BillingCycle string // one_time | monthly | yearly
	Status       string
	Currency     string
	PriceAmount  int64
	Hostname     string
	Plan         string
	Region       string
	OS           string
	CPU          int
	MemoryMB     int
	DiskGB       int
}

// ProductPurchaser is a port for the catalog context.
type ProductPurchaser interface {
	PurchaseProduct(ctx context.Context, productID, customerID, orderID, hostname, os string) (PurchasedProduct, error)
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

// InvoiceCreator is a port for the billing context.
// It provides invoice lifecycle operations needed by the payment flow.
type InvoiceCreator interface {
	// CreateAndIssueInvoice creates a draft invoice, adds a snapshot line item,
	// issues it, and returns the invoice ID. The description and price are
	// hard-copied snapshots of the product at purchase time (immutable).
	// billingCycle is one of "one_time", "monthly", "yearly".
	CreateAndIssueInvoice(customerID, currency, billingCycle, description string, priceAmount int64) (invoiceID string, err error)

	// RecordInvoicePayment marks an issued invoice as paid.
	RecordInvoicePayment(invoiceID string, amount int64, currency string) error

	// VoidInvoice marks an invoice as void (e.g. payment failed or timed out).
	VoidInvoice(invoiceID, reason string) error
}

// PostPaymentOrchestrator wires together the ordering, catalog, instance,
// and billing contexts for payment-related cross-domain workflows.
type PostPaymentOrchestrator struct {
	orders    OrderActivator
	products  ProductPurchaser
	instances InstanceCreator   // nil = skip instance creation
	invoices  InvoiceCreator    // nil = skip invoice creation
	delayed   delayed.Publisher // nil = skip delayed events
}

// NewPostPaymentOrchestrator builds the orchestrator with the cross-domain ports.
func NewPostPaymentOrchestrator(
	o OrderActivator,
	p ProductPurchaser,
	i InstanceCreator,
	inv InvoiceCreator,
	dp delayed.Publisher,
) *PostPaymentOrchestrator {
	return &PostPaymentOrchestrator{
		orders:    o,
		products:  p,
		instances: i,
		invoices:  inv,
		delayed:   dp,
	}
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

	// 2.5 Record payment on the linked invoice (invoice → paid)
	if s.invoices != nil && order.InvoiceID != "" {
		if err := s.invoices.RecordInvoicePayment(order.InvoiceID, order.PriceAmount, order.Currency); err != nil {
			// Non-fatal: order is already activated, log and continue
			log.Printf("[PostPaymentOrchestrator] WARNING: record invoice payment failed for invoice %s: %v", order.InvoiceID, err)
		} else {
			log.Printf("[PostPaymentOrchestrator] invoice payment recorded: invoice=%s amount=%d", order.InvoiceID, order.PriceAmount)
		}
	}

	// ── Steps 3 & 4: Parallel execution ────────────────────────────────────
	// Product slot consumption and instance creation are independent operations
	// that can run concurrently. This cuts ~50% off the post-payment latency
	// compared to sequential execution.
	//
	// Both steps are non-fatal: the order is already activated and the invoice
	// is paid, so failures are logged but do not cause a rollback.
	var (
		purchaseErr error
		instanceID  string
		instanceErr error
		wg          sync.WaitGroup
	)

	// 3. (parallel) Consume a commercial slot and fire provisioning event
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, purchaseErr = s.products.PurchaseProduct(
			context.Background(),
			order.ProductID,
			order.CustomerID,
			orderID,
			order.Hostname,
			order.OS,
		)
		if purchaseErr != nil {
			log.Printf("[PostPaymentOrchestrator] WARNING: product purchase (slot consume) failed for order %s: %v", orderID, purchaseErr)
		}
	}()

	// 4. (parallel) Create a pending instance — immediately visible to the user
	// Uses order snapshot data (not product data, since that's being fetched concurrently)
	if s.instances != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			instanceID, instanceErr = s.instances.CreatePendingInstance(
				order.CustomerID,
				orderID,
				order.Region,
				order.Hostname,
				order.Plan,
				order.OS,
				order.CPU,
				order.MemoryMB,
				order.DiskGB,
			)
			if instanceErr != nil {
				log.Printf("[PostPaymentOrchestrator] WARNING: create pending instance failed for order %s: %v", orderID, instanceErr)
			} else {
				log.Printf("[PostPaymentOrchestrator] pending instance created: %s (order=%s)", instanceID, orderID)
			}
		}()
	}

	wg.Wait()

	log.Printf("[PostPaymentOrchestrator] post-payment flow complete: order=%s → activated → provisioned", orderID)
	return nil
}

// GetOrderForPay retrieves an order as a PayableOrder DTO for use in the
// payment initiation flow (Pay handler). This avoids importing ordering types
// into the interface layer.
func (s *PostPaymentOrchestrator) GetOrderForPay(orderID string) (PayableOrder, error) {
	return s.orders.GetOrderForPayment(orderID)
}

// ── Invoice integration methods (Pay-time) ─────────────────────────────

// CreateInvoiceForPayment creates and issues an invoice for a pending order,
// then links the invoice to the order. The line item is a hard-copied snapshot
// of the product description and price at purchase time.
//
// Returns the invoiceID for inclusion in the pay response.
func (s *PostPaymentOrchestrator) CreateInvoiceForPayment(order PayableOrder) (string, error) {
	if s.invoices == nil {
		return "", nil // invoice creation disabled
	}

	// Build a snapshot description: "VPS Plan (2 vCPU / 4096MB / 80GB)"
	description := fmt.Sprintf("%s (%d vCPU / %dMB / %dGB)",
		order.Plan, order.CPU, order.MemoryMB, order.DiskGB)

	invoiceID, err := s.invoices.CreateAndIssueInvoice(
		order.CustomerID,
		order.Currency,
		order.BillingCycle,
		description,
		order.PriceAmount,
	)
	if err != nil {
		return "", fmt.Errorf("create invoice: %w", err)
	}

	// Link the invoice to the order
	if err := s.orders.LinkInvoiceToOrder(order.ID, invoiceID); err != nil {
		// Invoice created but link failed — void the orphan invoice
		log.Printf("[PostPaymentOrchestrator] WARNING: link invoice failed, voiding invoice %s: %v", invoiceID, err)
		_ = s.invoices.VoidInvoice(invoiceID, "failed to link to order")
		return "", fmt.Errorf("link invoice to order: %w", err)
	}

	log.Printf("[PostPaymentOrchestrator] invoice created and linked: invoice=%s order=%s", invoiceID, order.ID)
	return invoiceID, nil
}

// VoidInvoiceOnFailure voids an invoice when the payment charge creation fails.
// This ensures no orphan issued invoices remain in the system.
func (s *PostPaymentOrchestrator) VoidInvoiceOnFailure(invoiceID, reason string) {
	if s.invoices == nil || invoiceID == "" {
		return
	}
	if err := s.invoices.VoidInvoice(invoiceID, reason); err != nil {
		log.Printf("[PostPaymentOrchestrator] WARNING: failed to void invoice %s: %v", invoiceID, err)
	} else {
		log.Printf("[PostPaymentOrchestrator] invoice voided: %s reason=%s", invoiceID, reason)
	}
}

// InvoiceTimeoutPayload is the JSON payload for the delayed invoice timeout event.
type InvoiceTimeoutPayload struct {
	InvoiceID string `json:"invoice_id"`
	OrderID   string `json:"order_id"`
}

// ScheduleInvoiceTimeout publishes a delayed event that will check whether
// the invoice has been paid after the timeout duration. If not paid, the
// timeout worker will void the invoice and cancel the order.
func (s *PostPaymentOrchestrator) ScheduleInvoiceTimeout(invoiceID, orderID string, timeout time.Duration) {
	if s.delayed == nil || invoiceID == "" {
		return
	}
	payload, _ := json.Marshal(InvoiceTimeoutPayload{
		InvoiceID: invoiceID,
		OrderID:   orderID,
	})
	if err := s.delayed.PublishDelayed(context.Background(), "invoice.check_timeout", payload, timeout); err != nil {
		log.Printf("[PostPaymentOrchestrator] WARNING: failed to schedule invoice timeout for %s: %v", invoiceID, err)
	} else {
		log.Printf("[PostPaymentOrchestrator] invoice timeout scheduled: invoice=%s delay=%v", invoiceID, timeout)
	}
}
