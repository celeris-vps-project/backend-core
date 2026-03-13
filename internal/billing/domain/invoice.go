package domain

import (
	"errors"
	"time"
)

const (
	InvoiceStatusDraft  = "draft"
	InvoiceStatusIssued = "issued"
	InvoiceStatusPaid   = "paid"
	InvoiceStatusVoid   = "void"
)

// Invoice is the aggregate root for billing.
type Invoice struct {
	id         string
	customerID string
	currency   string
	status     string

	billingCycle BillingCycle
	periodStart  *time.Time
	periodEnd    *time.Time

	lineItems  []LineItem
	subtotal   Money
	tax        Money
	total      Money
	amountPaid Money

	issuedAt   *time.Time
	dueAt      *time.Time
	paidAt     *time.Time
	voidReason string
}

func NewDraftInvoice(id, customerID, currency string, billingCycle BillingCycle, periodStart, periodEnd *time.Time) (*Invoice, error) {
	if id == "" {
		return nil, errors.New("domain_error: invoice id is required")
	}
	if customerID == "" {
		return nil, errors.New("domain_error: customer id is required")
	}
	if currency == "" {
		return nil, errors.New("domain_error: currency is required")
	}
	if billingCycle.IsZero() {
		billingCycle = OneTimeCycle()
	}
	if billingCycle.IsRecurring() {
		if periodStart == nil || periodEnd == nil {
			return nil, errors.New("domain_error: recurring invoices require period start and end")
		}
		if !periodEnd.After(*periodStart) {
			return nil, errors.New("domain_error: period end must be after period start")
		}
	}

	zero := ZeroMoney(currency)
	return &Invoice{
		id:           id,
		customerID:   customerID,
		currency:     currency,
		status:       InvoiceStatusDraft,
		billingCycle: billingCycle,
		periodStart:  periodStart,
		periodEnd:    periodEnd,
		lineItems:    []LineItem{},
		subtotal:     zero,
		tax:          zero,
		total:        zero,
		amountPaid:   zero,
	}, nil
}

// ReconstituteInvoice rebuilds the aggregate from persistence.
func ReconstituteInvoice(
	id, customerID, currency, status string,
	billingCycle BillingCycle,
	periodStart, periodEnd *time.Time,
	lineItems []LineItem,
	subtotal, tax, total, amountPaid Money,
	issuedAt, dueAt, paidAt *time.Time,
	voidReason string,
) *Invoice {
	return &Invoice{
		id:           id,
		customerID:   customerID,
		currency:     currency,
		status:       status,
		billingCycle: billingCycle,
		periodStart:  periodStart,
		periodEnd:    periodEnd,
		lineItems:    lineItems,
		subtotal:     subtotal,
		tax:          tax,
		total:        total,
		amountPaid:   amountPaid,
		issuedAt:     issuedAt,
		dueAt:        dueAt,
		paidAt:       paidAt,
		voidReason:   voidReason,
	}
}

func (i *Invoice) ID() string         { return i.id }
func (i *Invoice) CustomerID() string { return i.customerID }
func (i *Invoice) Currency() string   { return i.currency }
func (i *Invoice) Status() string     { return i.status }

func (i *Invoice) BillingCycle() BillingCycle { return i.billingCycle }
func (i *Invoice) PeriodStart() *time.Time   { return i.periodStart }
func (i *Invoice) PeriodEnd() *time.Time     { return i.periodEnd }
func (i *Invoice) IsRecurring() bool         { return i.billingCycle.IsRecurring() }

func (i *Invoice) LineItems() []LineItem {
	items := make([]LineItem, len(i.lineItems))
	copy(items, i.lineItems)
	return items
}

func (i *Invoice) Subtotal() Money   { return i.subtotal }
func (i *Invoice) Tax() Money        { return i.tax }
func (i *Invoice) Total() Money      { return i.total }
func (i *Invoice) AmountPaid() Money { return i.amountPaid }

func (i *Invoice) IssuedAt() *time.Time { return i.issuedAt }
func (i *Invoice) DueAt() *time.Time    { return i.dueAt }
func (i *Invoice) PaidAt() *time.Time   { return i.paidAt }
func (i *Invoice) VoidReason() string   { return i.voidReason }

func (i *Invoice) AddLineItem(item LineItem) error {
	if i.status != InvoiceStatusDraft {
		return errors.New("domain_error: line items can only be added in draft")
	}
	if item.UnitPrice().Currency() != i.currency {
		return errors.New("domain_error: line item currency mismatch")
	}
	if i.hasLineItem(item.ID()) {
		return errors.New("domain_error: line item id already exists")
	}
	i.lineItems = append(i.lineItems, item)
	return i.recalculateTotals()
}

func (i *Invoice) SetTaxAmount(tax Money) error {
	if i.status != InvoiceStatusDraft {
		return errors.New("domain_error: tax can only be set in draft")
	}
	if tax.Currency() != i.currency {
		return errors.New("domain_error: tax currency mismatch")
	}
	i.tax = tax
	return i.recalculateTotals()
}

func (i *Invoice) Issue(issuedAt time.Time, dueAt *time.Time) error {
	if i.status != InvoiceStatusDraft {
		return errors.New("domain_error: only draft invoices can be issued")
	}
	if len(i.lineItems) == 0 {
		return errors.New("domain_error: invoice must contain at least one line item")
	}
	if i.total.IsZero() {
		return errors.New("domain_error: invoice total must be greater than zero")
	}
	if dueAt != nil && dueAt.Before(issuedAt) {
		return errors.New("domain_error: due date must be on or after issued date")
	}
	i.status = InvoiceStatusIssued
	i.issuedAt = &issuedAt
	if dueAt != nil {
		copyDue := *dueAt
		i.dueAt = &copyDue
	}
	return nil
}

func (i *Invoice) RecordPayment(amount Money, paidAt time.Time) error {
	if i.status != InvoiceStatusIssued {
		return errors.New("domain_error: payments can only be recorded on issued invoices")
	}
	if amount.Currency() != i.currency {
		return errors.New("domain_error: payment currency mismatch")
	}
	if amount.IsZero() {
		return errors.New("domain_error: payment amount must be greater than zero")
	}

	updated, err := i.amountPaid.Add(amount)
	if err != nil {
		return err
	}
	i.amountPaid = updated

	paidInFull, err := i.amountPaid.GreaterThanOrEqual(i.total)
	if err != nil {
		return err
	}
	if paidInFull {
		i.status = InvoiceStatusPaid
		i.paidAt = &paidAt
	}
	return nil
}

func (i *Invoice) Void(reason string) error {
	if i.status == InvoiceStatusPaid {
		return errors.New("domain_error: paid invoices cannot be voided")
	}
	if i.status == InvoiceStatusVoid {
		return errors.New("domain_error: invoice already void")
	}
	if reason == "" {
		return errors.New("domain_error: void reason is required")
	}
	i.status = InvoiceStatusVoid
	i.voidReason = reason
	return nil
}

func (i *Invoice) recalculateTotals() error {
	subtotal := ZeroMoney(i.currency)
	for _, item := range i.lineItems {
		itemTotal, err := item.Total()
		if err != nil {
			return err
		}
		subtotal, err = subtotal.Add(itemTotal)
		if err != nil {
			return err
		}
	}
	i.subtotal = subtotal
	total, err := subtotal.Add(i.tax)
	if err != nil {
		return err
	}
	i.total = total
	return nil
}

func (i *Invoice) hasLineItem(itemID string) bool {
	for _, item := range i.lineItems {
		if item.ID() == itemID {
			return true
		}
	}
	return false
}
