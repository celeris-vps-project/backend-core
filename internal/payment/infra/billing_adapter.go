package infra

import (
	billingApp "backend-core/internal/billing/app"
	"backend-core/internal/billing/domain"
	paymentApp "backend-core/internal/payment/app"
	"fmt"
	"time"
)

// BillingAdapter implements paymentApp.InvoiceCreator by wrapping the
// billing context's InvoiceAppService. It encapsulates the multi-step
// invoice creation flow (draft → add line item → issue) into a single
// atomic operation from the payment context's perspective.
//
// All values written to the invoice (description, price) are hard-copied
// snapshots at purchase time — they are never referenced back to the
// catalog product, ensuring financial record immutability.
type BillingAdapter struct {
	svc *billingApp.InvoiceAppService
}

// NewBillingAdapter wraps an InvoiceAppService as an InvoiceCreator.
func NewBillingAdapter(svc *billingApp.InvoiceAppService) *BillingAdapter {
	return &BillingAdapter{svc: svc}
}

// CreateAndIssueInvoice performs the full invoice creation flow:
//  1. Create a draft invoice for the customer (with billing cycle + period)
//  2. Add a single line item with the product snapshot (description + price)
//  3. Issue the invoice (draft → issued)
//
// Returns the invoice ID. The line item description and unit price are
// immutable snapshots — future product changes do not affect this invoice.
func (a *BillingAdapter) CreateAndIssueInvoice(
	customerID, currency, billingCycle, description string, priceAmount int64,
) (string, error) {
	// 1. Build billing cycle value object
	cycle, err := domain.NewBillingCycle(billingCycle)
	if err != nil {
		return "", fmt.Errorf("billing: invalid billing cycle: %w", err)
	}

	// For recurring invoices, set the first period starting now.
	var periodStart, periodEnd *time.Time
	if cycle.IsRecurring() {
		now := time.Now().UTC()
		// Align period to beginning of current day
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		_, end := cycle.NextPeriod(start)
		periodStart = &start
		periodEnd = &end
	}

	// 2. Create draft invoice
	invoice, err := a.svc.CreateDraft(customerID, currency, cycle, periodStart, periodEnd)
	if err != nil {
		return "", fmt.Errorf("billing: create draft: %w", err)
	}

	// 3. Add line item — snapshot of product at purchase time
	unitPrice, err := domain.NewMoney(currency, priceAmount)
	if err != nil {
		return "", fmt.Errorf("billing: invalid price: %w", err)
	}

	// Use invoice ID + "-item-1" as the line item ID for deterministic naming
	lineItemID := invoice.ID() + "-item-1"
	lineItem, err := domain.NewLineItem(lineItemID, description, 1, unitPrice)
	if err != nil {
		return "", fmt.Errorf("billing: create line item: %w", err)
	}

	if err := a.svc.AddLineItem(invoice.ID(), lineItem); err != nil {
		return "", fmt.Errorf("billing: add line item: %w", err)
	}

	// 4. Issue the invoice (draft → issued)
	issuedAt := time.Now()
	dueAt := issuedAt.Add(30 * 24 * time.Hour) // 30-day payment window
	if err := a.svc.IssueInvoice(invoice.ID(), issuedAt, &dueAt); err != nil {
		return "", fmt.Errorf("billing: issue invoice: %w", err)
	}

	return invoice.ID(), nil
}

// RecordInvoicePayment records a payment on an issued invoice, transitioning
// it to "paid" status if the full amount is covered.
func (a *BillingAdapter) RecordInvoicePayment(invoiceID string, amount int64, currency string) error {
	payAmount, err := domain.NewMoney(currency, amount)
	if err != nil {
		return fmt.Errorf("billing: invalid payment amount: %w", err)
	}
	paidAt := time.Now()
	return a.svc.RecordPayment(invoiceID, payAmount, paidAt)
}

// VoidInvoice marks an invoice as void with the given reason.
func (a *BillingAdapter) VoidInvoice(invoiceID, reason string) error {
	return a.svc.VoidInvoice(invoiceID, reason)
}

// GetInvoiceStatus returns the current status of an invoice.
func (a *BillingAdapter) GetInvoiceStatus(invoiceID string) (string, error) {
	invoice, err := a.svc.GetInvoice(invoiceID)
	if err != nil {
		return "", err
	}
	return invoice.Status(), nil
}

func (a *BillingAdapter) GetInvoice(invoiceID string) (paymentApp.RenewalInvoice, error) {
	invoice, err := a.svc.GetInvoice(invoiceID)
	if err != nil {
		return paymentApp.RenewalInvoice{}, err
	}
	return paymentApp.RenewalInvoice{
		ID:        invoice.ID(),
		Status:    invoice.Status(),
		PeriodEnd: invoice.PeriodEnd(),
		DueAt:     invoice.DueAt(),
	}, nil
}

func (a *BillingAdapter) GenerateRenewalInvoice(sourceInvoiceID string) (paymentApp.RenewalInvoice, error) {
	invoice, err := a.svc.GenerateRenewalInvoice(sourceInvoiceID)
	if err != nil {
		return paymentApp.RenewalInvoice{}, err
	}
	return paymentApp.RenewalInvoice{
		ID:        invoice.ID(),
		Status:    invoice.Status(),
		PeriodEnd: invoice.PeriodEnd(),
		DueAt:     invoice.DueAt(),
	}, nil
}

func (a *BillingAdapter) IssueInvoice(invoiceID string, issuedAt time.Time, dueAt *time.Time) error {
	return a.svc.IssueInvoice(invoiceID, issuedAt, dueAt)
}

// Compile-time interface check
var _ paymentApp.InvoiceCreator = (*BillingAdapter)(nil)
var _ paymentApp.RenewalInvoiceManager = (*BillingAdapter)(nil)
