package infra

import (
	paymentApp "backend-core/internal/payment/app"
	"encoding/json"
	"log"
	"time"
)

// InvoiceTimeoutWorker handles delayed "invoice.check_timeout" events.
// It delegates all cross-domain logic to the PostPaymentOrchestrator,
// keeping itself as a thin infra adapter (unmarshal → delegate).
//
// Idempotent by design (guaranteed by the orchestrator):
//   - If invoice is already "paid" → no-op
//   - If invoice is already "void" → no-op
//   - If invoice is "issued" (still unpaid) → void invoice + cancel order
type InvoiceTimeoutWorker struct {
	orchestrator *paymentApp.PostPaymentOrchestrator
}

// NewInvoiceTimeoutWorker creates a timeout worker that delegates to the
// orchestrator for all cross-domain operations.
func NewInvoiceTimeoutWorker(orchestrator *paymentApp.PostPaymentOrchestrator) *InvoiceTimeoutWorker {
	return &InvoiceTimeoutWorker{orchestrator: orchestrator}
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

	w.orchestrator.HandleInvoiceTimeout(msg.InvoiceID, msg.OrderID)
}

// HandlerFunc returns a function compatible with InMemoryDelayedPublisher's callback.
func (w *InvoiceTimeoutWorker) HandlerFunc() func(topic string, payload []byte) {
	return w.Handle
}

// DefaultInvoiceTimeout is the default duration after which an unpaid invoice
// is automatically voided. This can be overridden via configuration.
const DefaultInvoiceTimeout = 30 * time.Minute
