package domain

import (
	"errors"
	"time"
)

const (
	OrderStatusPending    = "pending"    // awaiting payment
	OrderStatusActive     = "active"     // paid & provisioned
	OrderStatusSuspended  = "suspended"  // e.g. overdue payment
	OrderStatusCancelled  = "cancelled"  // customer-initiated cancel
	OrderStatusTerminated = "terminated" // admin-terminated
)

// Order is the aggregate root for VPS orders.
type Order struct {
	id           string
	customerID   string
	productID    string // FK to the product being purchased
	invoiceID    string // associated billing invoice
	billingCycle string // one_time | monthly | yearly — records the customer's purchase intent
	vpsConfig    VPSConfig
	status       string
	currency     string
	priceAmount  int64 // minor-unit amount per billing cycle

	createdAt    time.Time
	activatedAt  *time.Time
	suspendedAt  *time.Time
	cancelledAt  *time.Time
	terminatedAt *time.Time
	cancelReason string
}

func NewOrder(id, customerID, productID, invoiceID, billingCycle string, cfg VPSConfig, currency string, priceAmount int64) (*Order, error) {
	if id == "" {
		return nil, errors.New("domain_error: order id is required")
	}
	if customerID == "" {
		return nil, errors.New("domain_error: customer id is required")
	}
	if productID == "" {
		return nil, errors.New("domain_error: product id is required")
	}
	if currency == "" {
		return nil, errors.New("domain_error: currency is required")
	}
	if priceAmount <= 0 {
		return nil, errors.New("domain_error: price must be > 0")
	}
	if billingCycle == "" {
		billingCycle = "one_time"
	}

	return &Order{
		id:           id,
		customerID:   customerID,
		productID:    productID,
		invoiceID:    invoiceID,
		billingCycle: billingCycle,
		vpsConfig:    cfg,
		status:       OrderStatusPending,
		currency:     currency,
		priceAmount:  priceAmount,
		createdAt:    time.Now(),
	}, nil
}

// ReconstituteOrder rebuilds the aggregate from persistence.
func ReconstituteOrder(
	id, customerID, productID, invoiceID, billingCycle string,
	cfg VPSConfig,
	status, currency string,
	priceAmount int64,
	createdAt time.Time,
	activatedAt, suspendedAt, cancelledAt, terminatedAt *time.Time,
	cancelReason string,
) *Order {
	if billingCycle == "" {
		billingCycle = "one_time"
	}
	return &Order{
		id:           id,
		customerID:   customerID,
		productID:    productID,
		invoiceID:    invoiceID,
		billingCycle: billingCycle,
		vpsConfig:    cfg,
		status:       status,
		currency:     currency,
		priceAmount:  priceAmount,
		createdAt:    createdAt,
		activatedAt:  activatedAt,
		suspendedAt:  suspendedAt,
		cancelledAt:  cancelledAt,
		terminatedAt: terminatedAt,
		cancelReason: cancelReason,
	}
}

// ---- Read accessors ----

func (o *Order) ID() string           { return o.id }
func (o *Order) CustomerID() string   { return o.customerID }
func (o *Order) ProductID() string    { return o.productID }
func (o *Order) InvoiceID() string    { return o.invoiceID }
func (o *Order) BillingCycle() string  { return o.billingCycle }
func (o *Order) VPSConfig() VPSConfig { return o.vpsConfig }
func (o *Order) Status() string       { return o.status }
func (o *Order) Currency() string     { return o.currency }
func (o *Order) PriceAmount() int64   { return o.priceAmount }

func (o *Order) CreatedAt() time.Time     { return o.createdAt }
func (o *Order) ActivatedAt() *time.Time  { return o.activatedAt }
func (o *Order) SuspendedAt() *time.Time  { return o.suspendedAt }
func (o *Order) CancelledAt() *time.Time  { return o.cancelledAt }
func (o *Order) TerminatedAt() *time.Time { return o.terminatedAt }
func (o *Order) CancelReason() string     { return o.cancelReason }

// ---- Domain transitions ----

// Activate moves the order from pending → active (payment confirmed, provisioned).
func (o *Order) Activate(at time.Time) error {
	if o.status != OrderStatusPending {
		return errors.New("domain_error: only pending orders can be activated")
	}
	o.status = OrderStatusActive
	o.activatedAt = &at
	return nil
}

// Suspend pauses a running order (e.g. overdue).
func (o *Order) Suspend(at time.Time) error {
	if o.status != OrderStatusActive {
		return errors.New("domain_error: only active orders can be suspended")
	}
	o.status = OrderStatusSuspended
	o.suspendedAt = &at
	return nil
}

// Unsuspend re-activates a suspended order.
func (o *Order) Unsuspend(at time.Time) error {
	if o.status != OrderStatusSuspended {
		return errors.New("domain_error: only suspended orders can be unsuspended")
	}
	o.status = OrderStatusActive
	o.activatedAt = &at
	o.suspendedAt = nil
	return nil
}

// Cancel is a customer-initiated cancellation (allowed from pending/active/suspended).
func (o *Order) Cancel(reason string, at time.Time) error {
	if o.status == OrderStatusCancelled {
		return errors.New("domain_error: order already cancelled")
	}
	if o.status == OrderStatusTerminated {
		return errors.New("domain_error: terminated orders cannot be cancelled")
	}
	if reason == "" {
		return errors.New("domain_error: cancel reason is required")
	}
	o.status = OrderStatusCancelled
	o.cancelReason = reason
	o.cancelledAt = &at
	return nil
}

// LinkInvoice associates a billing invoice with this order.
// This can only be done while the order is in pending status (before payment).
func (o *Order) LinkInvoice(invoiceID string) error {
	if invoiceID == "" {
		return errors.New("domain_error: invoice id is required")
	}
	if o.status != OrderStatusPending {
		return errors.New("domain_error: invoice can only be linked to pending orders")
	}
	o.invoiceID = invoiceID
	return nil
}

// Terminate is an admin-initiated termination (allowed from any non-terminated state).
func (o *Order) Terminate(at time.Time) error {
	if o.status == OrderStatusTerminated {
		return errors.New("domain_error: order already terminated")
	}
	o.status = OrderStatusTerminated
	o.terminatedAt = &at
	return nil
}
