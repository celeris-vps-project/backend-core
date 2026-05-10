package app

import (
	"backend-core/pkg/delayed"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// PostPaymentOrchestrator orchestrates the cross-domain flow after a
// payment is confirmed via webhook:
//  1. Activate the order (ordering context)
//  2. Create a pending instance (instance context)
//  3. Consume product stock and trigger provisioning (catalog/provisioning)
//
// The instance context owns the fulfillment identity. Provisioning must reuse
// that instanceID so async callbacks update the same record visible to users.

// OrderActivator is a port for the ordering context.
type OrderActivator interface {
	ActivateOrder(orderID string) error
	GetOrderForPayment(orderID string) (PayableOrder, error)
	ListOrders() ([]PayableOrder, error)
	LinkInvoiceToOrder(orderID, invoiceID string) error
	CancelOrder(orderID, reason string) error
}

// PayableOrder is the minimal read-model the payment context needs.
type PayableOrder struct {
	ID           string
	CustomerID   string
	ProductID    string
	InvoiceID    string
	BillingCycle string // one_time | monthly | yearly
	Status       string //
	Currency     string
	PriceAmount  int64
	Hostname     string
	Plan         string
	Region       string
	OS           string
	NetworkMode  string
	CPU          int
	MemoryMB     int
	DiskGB       int
	BandwidthGB  int
}

// ProductPurchaser is a port for the catalog context.
type ProductPurchaser interface {
	PurchaseProduct(ctx context.Context, productID, customerID, orderID, instanceID, initialPassword, hostname, os, networkMode string) (PurchasedProduct, error)
	ReserveProduct(ctx context.Context, productID string) error
	ReleaseProduct(ctx context.Context, productID string) error
}

// PurchasedProduct is the minimal read-model returned after purchase.
type PurchasedProduct struct {
	Location    string
	Slug        string
	CPU         int
	MemoryMB    int
	DiskGB      int
	BandwidthGB int
}

// InstanceCreator is a port for the instance context.
// Returns the newly created instance delivery details or an error.
type InstanceCreator interface {
	CreatePendingInstance(customerID, orderID, region, hostname, plan, os, networkMode string, cpu, memoryMB, diskGB, bandwidthGB int) (PendingInstance, error)
}

type PendingInstance struct {
	ID              string
	InitialPassword string
}

// InvoiceCreator is a port for the billing context.
type InvoiceCreator interface {
	CreateAndIssueInvoice(customerID, currency, billingCycle, description string, priceAmount int64) (invoiceID string, err error)
	RecordInvoicePayment(invoiceID string, amount int64, currency string) error
	VoidInvoice(invoiceID, reason string) error
	GetInvoiceStatus(invoiceID string) (string, error)
	GetInvoiceForPayment(invoiceID string) (PayableInvoice, error)
}

type CouponReleaser interface {
	ActivateCodeAfterPayment(orderID string) error
	ReleaseCoupon(orderID string) error
}

type PayableInvoice struct {
	ID       string
	Status   string
	Currency string
	Total    int64
}

// PostPaymentOrchestrator wires together the ordering, catalog, instance,
// and billing contexts for payment-related cross-domain workflows.
type PostPaymentOrchestrator struct {
	orders    OrderActivator
	products  ProductPurchaser
	instances InstanceCreator // nil = skip instance creation
	invoices  InvoiceCreator  // nil = skip invoice creation
	coupon    CouponReleaser
	delayed   delayed.Publisher // nil = skip delayed events
}

// NewPostPaymentOrchestrator builds the orchestrator with the cross-domain ports.
func NewPostPaymentOrchestrator(
	o OrderActivator,
	p ProductPurchaser,
	i InstanceCreator,
	inv InvoiceCreator,
	cp CouponReleaser,
	dp delayed.Publisher,
) *PostPaymentOrchestrator {
	return &PostPaymentOrchestrator{
		orders:    o,
		products:  p,
		instances: i,
		invoices:  inv,
		coupon:    cp,
		delayed:   dp,
	}
}

// HandlePaymentConfirmed runs the full post-payment flow for a confirmed order.
//
// The pending instance is created first so provisioning can reuse the same
// instanceID when it later reports IP/NAT information back.
func (s *PostPaymentOrchestrator) HandlePaymentConfirmed(orderID string) error {
	// 1. Activate the order (pending -> active).
	if err := s.orders.ActivateOrder(orderID); err != nil {
		return fmt.Errorf("activate order: %w", err)
	}

	// 2. Reload the order to obtain VPS configuration for downstream steps.
	order, err := s.orders.GetOrderForPayment(orderID)
	if err != nil {
		return fmt.Errorf("get order after activation: %w", err)
	}

	// 2.5 Record payment on the linked invoice (invoice -> paid).
	if s.invoices != nil && order.InvoiceID != "" {
		if err := s.invoices.RecordInvoicePayment(order.InvoiceID, order.PriceAmount, order.Currency); err != nil {
			// Non-fatal: order is already activated, log and continue.
			log.Printf("[PostPaymentOrchestrator] WARNING: record invoice payment failed for invoice %s: %v", order.InvoiceID, err)
		} else {
			log.Printf("[PostPaymentOrchestrator] invoice payment recorded: invoice=%s amount=%d", order.InvoiceID, order.PriceAmount)
		}
	}

	// 3. Create the pending instance first so the provisioning callback can
	// update the same instance record instead of a controller-generated ID.
	instanceID := ""
	initialPassword := ""

	if s.instances != nil {
		pendingInstance, err := s.instances.CreatePendingInstance(
			order.CustomerID,
			orderID,
			order.Region,
			order.Hostname,
			order.Plan,
			order.OS,
			order.NetworkMode,
			order.CPU,
			order.MemoryMB,
			order.DiskGB,
			order.BandwidthGB,
		)
		if err != nil {
			return fmt.Errorf("create pending instance: %w", err)
		}
		instanceID = pendingInstance.ID
		initialPassword = pendingInstance.InitialPassword
		log.Printf("[PostPaymentOrchestrator] pending instance created: %s (order=%s)", instanceID, orderID)
	}

	if err := s.coupon.ActivateCodeAfterPayment(orderID); err != nil {
		log.Printf("[PostPaymentOrchestrator] activate code failed after payment confirmed: %s", instanceID)
	}

	// 4. Consume a commercial slot and trigger asynchronous provisioning using
	// the same instanceID when one already exists.
	if _, err := s.products.PurchaseProduct(
		context.Background(),
		order.ProductID,
		order.CustomerID,
		orderID,
		instanceID,
		initialPassword,
		order.Hostname,
		order.OS,
		order.NetworkMode,
	); err != nil {
		return fmt.Errorf("purchase product: %w", err)
	}

	log.Printf("[PostPaymentOrchestrator] post-payment flow complete: order=%s instance=%s -> activated -> provision triggered", orderID, instanceID)
	return nil
}

// GetOrderForPay retrieves an order as a PayableOrder DTO for use in the
// payment initiation flow (Pay handler). This avoids importing ordering types
// into the interface layer.
func (s *PostPaymentOrchestrator) GetOrderForPay(orderID string) (PayableOrder, error) {
	return s.orders.GetOrderForPayment(orderID)
}

func (s *PostPaymentOrchestrator) ListOrdersForPay() ([]PayableOrder, error) {
	return s.orders.ListOrders()
}

func (s *PostPaymentOrchestrator) GetInvoiceForPayment(invoiceID string) (PayableInvoice, error) {
	if s.invoices == nil {
		return PayableInvoice{}, fmt.Errorf("invoice service not configured")
	}
	return s.invoices.GetInvoiceForPayment(invoiceID)
}

// CreateInvoiceForPayment creates and issues an invoice for a pending order,
// then links the invoice to the order. The line item is a hard-copied snapshot
// of the product description and price at purchase time.
func (s *PostPaymentOrchestrator) CreateInvoiceForPayment(order PayableOrder) (string, error) {
	if s.invoices == nil {
		return "", nil // invoice creation disabled
	}

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

	// Link the invoice to the order.
	if err := s.orders.LinkInvoiceToOrder(order.ID, invoiceID); err != nil {
		// Invoice created but link failed; void the orphan invoice.
		log.Printf("[PostPaymentOrchestrator] WARNING: link invoice failed, voiding invoice %s: %v", invoiceID, err)
		_ = s.invoices.VoidInvoice(invoiceID, "failed to link to order")
		return "", fmt.Errorf("link invoice to order: %w", err)
	}

	log.Printf("[PostPaymentOrchestrator] invoice created and linked: invoice=%s order=%s", invoiceID, order.ID)
	return invoiceID, nil
}

func (s *PostPaymentOrchestrator) ReserveProductForPayment(productID string) error {
	if s.products == nil || productID == "" {
		return nil
	}
	return s.products.ReserveProduct(context.Background(), productID)
}

func (s *PostPaymentOrchestrator) ReleaseReservedProduct(productID, orderID, reason string) {
	s.releaseReservedProduct(productID, orderID, reason)
}

// VoidInvoiceOnFailure only voids the current invoice for an unpaid pending order.
// This keeps normal first-payment retries clean without breaking renewal invoices.
// When the first-payment invoice is voided, the reserved product slot is released.
func (s *PostPaymentOrchestrator) VoidInvoiceOnFailure(order PayableOrder, invoiceID, reason string) {
	if order.Status != "pending" {
		log.Printf("[PostPaymentOrchestrator] skipping invoice void for order=%s status=%s invoice=%s",
			order.ID, order.Status, invoiceID)
		return
	}
	if order.InvoiceID == "" || order.InvoiceID != invoiceID {
		log.Printf("[PostPaymentOrchestrator] skipping invoice void for order=%s current_invoice=%s failed_invoice=%s",
			order.ID, order.InvoiceID, invoiceID)
		return
	}
	if s.voidInvoiceIfOpen(invoiceID, reason) {
		log.Printf("[PostPaymentOrchestrator] invoice voided: %s reason=%s", invoiceID, reason)
		s.releaseReservedProduct(order.ProductID, order.ID, reason)
	}
}

// InvoiceTimeoutPayload is the JSON payload for the delayed invoice timeout event.
type InvoiceTimeoutPayload struct {
	InvoiceID string `json:"invoice_id"`
	OrderID   string `json:"order_id"`
}

type invoiceTimeoutPlan struct {
	order          PayableOrder
	hasOrder       bool
	staleInvoice   bool
	voidReason     string
	releaseProduct bool
	cancelOrder    bool
}

func (s *PostPaymentOrchestrator) releaseReservedProduct(productID, orderID, reason string) {
	if s.products == nil || productID == "" {
		return
	}
	if err := s.products.ReleaseProduct(context.Background(), productID); err != nil {
		log.Printf("[PostPaymentOrchestrator] ERROR: failed to release reserved product: order=%s product=%s reason=%s err=%v",
			orderID, productID, reason, err)
		return
	}
	log.Printf("[PostPaymentOrchestrator] reserved product released: order=%s product=%s reason=%s",
		orderID, productID, reason)
}

// HandleInvoiceTimeout checks whether an invoice has been paid after the
// timeout period. If still unpaid ("issued"), it voids the invoice and
// cancels the associated order.
func (s *PostPaymentOrchestrator) HandleInvoiceTimeout(invoiceID, orderID string) {
	plan, ok := s.planInvoiceTimeout(invoiceID, orderID)
	if !ok {
		return
	}
	s.applyInvoiceTimeoutPlan(invoiceID, orderID, plan)
}

func (s *PostPaymentOrchestrator) planInvoiceTimeout(invoiceID, orderID string) (invoiceTimeoutPlan, bool) {
	if s.invoices == nil || invoiceID == "" {
		return invoiceTimeoutPlan{}, false
	}

	log.Printf("[PostPaymentOrchestrator] checking timeout: invoice=%s order=%s", invoiceID, orderID)

	if orderID == "" {
		return invoiceTimeoutPlan{
			voidReason: "payment timeout - auto-voided after deadline",
		}, true
	}

	order, err := s.orders.GetOrderForPayment(orderID)
	if err != nil {
		log.Printf("[PostPaymentOrchestrator] WARNING: failed to load order %s for timeout check: %v", orderID, err)
		return invoiceTimeoutPlan{}, false
	}

	if order.InvoiceID == "" || order.InvoiceID != invoiceID {
		return invoiceTimeoutPlan{
			order:        order,
			hasOrder:     true,
			staleInvoice: true,
			voidReason:   "payment timeout - stale invoice auto-voided",
		}, true
	}

	if order.Status != "pending" {
		log.Printf("[PostPaymentOrchestrator] skipping timeout for order=%s status=%s invoice=%s",
			orderID, order.Status, invoiceID)
		return invoiceTimeoutPlan{}, false
	}

	return invoiceTimeoutPlan{
		order:          order,
		hasOrder:       true,
		voidReason:     "payment timeout - auto-voided after deadline",
		releaseProduct: true,
		cancelOrder:    true,
	}, true
}

func (s *PostPaymentOrchestrator) applyInvoiceTimeoutPlan(invoiceID, orderID string, plan invoiceTimeoutPlan) {
	if !s.voidInvoiceIfOpen(invoiceID, plan.voidReason) {
		if plan.staleInvoice {
			s.logStaleInvoiceTimeoutSkipped(invoiceID, orderID, plan.order.InvoiceID)
		}
		return
	}

	if plan.staleInvoice {
		log.Printf("[PostPaymentOrchestrator] stale invoice voided: invoice=%s order=%s current_invoice=%s",
			invoiceID, orderID, plan.order.InvoiceID)
		s.logStaleInvoiceTimeoutSkipped(invoiceID, orderID, plan.order.InvoiceID)
		return
	}

	log.Printf("[PostPaymentOrchestrator] invoice voided: %s (payment timeout)", invoiceID)
	if plan.releaseProduct && plan.hasOrder {
		// TODO:schedule 5 min for release in order to handle those user paid but order marked cancel

		s.releaseReservedProduct(plan.order.ProductID, plan.order.ID, "payment timeout")
		s.releaseCouponForOrder(plan.order.ID, "payment timeout")

	}
	if plan.cancelOrder {
		s.cancelOrderForTimeout(orderID)
	}
}

func (s *PostPaymentOrchestrator) logStaleInvoiceTimeoutSkipped(invoiceID, orderID, currentInvoiceID string) {
	log.Printf("[PostPaymentOrchestrator] skipping timeout cancellation for stale invoice=%s order=%s current_invoice=%s",
		invoiceID, orderID, currentInvoiceID)
}

func (s *PostPaymentOrchestrator) releaseCouponForOrder(orderID, reason string) {
	if s.coupon == nil || orderID == "" {
		return
	}
	if err := s.coupon.ReleaseCoupon(orderID); err != nil {
		log.Printf("[PostPaymentOrchestrator] WARNING: failed to release coupon redemption: order=%s reason=%s err=%v",
			orderID, reason, err)
		return
	}
	log.Printf("[PostPaymentOrchestrator] coupon redemption released: order=%s reason=%s", orderID, reason)
}

func (s *PostPaymentOrchestrator) cancelOrderForTimeout(orderID string) {
	if orderID == "" {
		return
	}
	if err := s.orders.CancelOrder(orderID, "payment timeout - invoice auto-voided"); err != nil {
		// Order might already be activated (race with webhook); that is fine.
		log.Printf("[PostPaymentOrchestrator] WARNING: failed to cancel order %s (may already be active): %v", orderID, err)
		return
	}
	log.Printf("[PostPaymentOrchestrator] order cancelled: %s (payment timeout)", orderID)
}

func (s *PostPaymentOrchestrator) voidInvoiceIfOpen(invoiceID, reason string) bool {
	if s.invoices == nil || invoiceID == "" {
		return false
	}

	status, err := s.invoices.GetInvoiceStatus(invoiceID)
	if err != nil {
		log.Printf("[PostPaymentOrchestrator] ERROR: invoice not found %s: %v", invoiceID, err)
		return false
	}

	switch status {
	case "paid":
		log.Printf("[PostPaymentOrchestrator] invoice %s already paid, skipping", invoiceID)
		return false
	case "void":
		log.Printf("[PostPaymentOrchestrator] invoice %s already void, skipping", invoiceID)
		return false
	}

	if err := s.invoices.VoidInvoice(invoiceID, reason); err != nil {
		log.Printf("[PostPaymentOrchestrator] ERROR: failed to void invoice %s: %v", invoiceID, err)
		return false
	}
	return true
}

// ScheduleInvoiceTimeout publishes a delayed event that will check whether
// the invoice has been paid after the timeout duration.
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
