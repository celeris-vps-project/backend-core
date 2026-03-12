package infra

import (
	billingApp "backend-core/internal/billing/app"
	billingDomain "backend-core/internal/billing/domain"
	orderingApp "backend-core/internal/ordering/app"
	paymentApp "backend-core/internal/payment/app"
	"encoding/json"
	"log"
	"time"
)

// InvoiceTimeoutWorker handles delayed "invoice.check_timeout" events.
// When fired, it checks whether the invoice has been paid. If not, it
// voids the invoice and cancels the associated order.
//
// Idempotent by design:
//   - If invoice is already "paid" → no-op
//   - If invoice is already "void" → no-op
//   - If invoice is "issued" (still unpaid) → void invoice + cancel order
type InvoiceTimeoutWorker struct {
	invoiceSvc *billingApp.InvoiceAppService
	orderSvc   *orderingApp.OrderAppService
}

// NewInvoiceTimeoutWorker creates a timeout worker.
func NewInvoiceTimeoutWorker(
	invoiceSvc *billingApp.InvoiceAppService,
	orderSvc *orderingApp.OrderAppService,
) *InvoiceTimeoutWorker {
	return &InvoiceTimeoutWorker{
		invoiceSvc: invoiceSvc,
		orderSvc:   orderSvc,
	}
}

// Handle processes a delayed invoice timeout event. This method is designed
// to be called by the DelayedEventPublisher's handler callback.
func (w *InvoiceTimeoutWorker) Handle(topic string, payload []byte) {
	if topic != "invoice.check_timeout" {
		return
	}

	var msg paymentApp.InvoiceTimeoutPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("[InvoiceTimeoutWorker] ERROR: failed to unmarshal payload: %v", err)
		return
	}

	log.Printf("[InvoiceTimeoutWorker] checking timeout: invoice=%s order=%s", msg.InvoiceID, msg.OrderID)

	// 1. Look up the invoice
	invoice, err := w.invoiceSvc.GetInvoice(msg.InvoiceID)
	if err != nil {
		log.Printf("[InvoiceTimeoutWorker] ERROR: invoice not found %s: %v", msg.InvoiceID, err)
		return
	}

	// 2. Idempotent check: if already paid or void, nothing to do
	switch invoice.Status() {
	case billingDomain.InvoiceStatusPaid:
		log.Printf("[InvoiceTimeoutWorker] invoice %s already paid, skipping", msg.InvoiceID)
		return
	case billingDomain.InvoiceStatusVoid:
		log.Printf("[InvoiceTimeoutWorker] invoice %s already void, skipping", msg.InvoiceID)
		return
	}

	// 3. Invoice is still issued (unpaid) — void it
	if err := w.invoiceSvc.VoidInvoice(msg.InvoiceID, "payment timeout — auto-voided after deadline"); err != nil {
		log.Printf("[InvoiceTimeoutWorker] ERROR: failed to void invoice %s: %v", msg.InvoiceID, err)
		return
	}
	log.Printf("[InvoiceTimeoutWorker] invoice voided: %s (payment timeout)", msg.InvoiceID)

	// 4. Cancel the associated order
	if msg.OrderID != "" {
		if err := w.orderSvc.CancelOrder(msg.OrderID, "payment timeout — invoice auto-voided"); err != nil {
			// Order might already be activated (race with webhook) — that's fine
			log.Printf("[InvoiceTimeoutWorker] WARNING: failed to cancel order %s (may already be active): %v", msg.OrderID, err)
		} else {
			log.Printf("[InvoiceTimeoutWorker] order cancelled: %s (payment timeout)", msg.OrderID)
		}
	}
}

// HandlerFunc returns a function compatible with InMemoryDelayedPublisher's callback.
func (w *InvoiceTimeoutWorker) HandlerFunc() func(topic string, payload []byte) {
	return w.Handle
}

// DefaultInvoiceTimeout is the default duration after which an unpaid invoice
// is automatically voided. This can be overridden via configuration.
const DefaultInvoiceTimeout = 30 * time.Minute
