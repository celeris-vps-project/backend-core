// Package delayed provides a generic delayed event publishing abstraction.
//
// The Publisher interface allows any bounded context to schedule future
// actions (e.g. invoice timeout auto-void, renewal reminders) without
// depending on a specific message broker or timer implementation.
//
// Implementations:
//   - InMemoryPublisher: goroutine + time.AfterFunc (single-instance, dev/MVP)
//   - AsynqPublisher:    Redis-backed persistent tasks (production, multi-instance)
//
// Business-specific handlers (e.g. InvoiceTimeoutWorker, RenewalTimeoutWorker)
// remain in their respective bounded contexts. Only the publishing
// infrastructure lives in this package.
package delayed

import (
	"context"
	"log"
	"time"
)

// Publisher abstracts a delayed message queue for scheduling future actions.
// Implementations can use in-memory timers, Asynq (Redis), RabbitMQ, or
// any message broker with delay support.
type Publisher interface {
	// PublishDelayed schedules a message to be delivered after the given delay.
	// The topic identifies the event type; the payload is an opaque byte slice
	// (typically JSON) that the subscriber will unmarshal.
	PublishDelayed(ctx context.Context, topic string, payload []byte, delay time.Duration) error
}

// HandlerFunc is the callback signature for delayed event subscribers.
type HandlerFunc func(topic string, payload []byte)

// ── InMemoryPublisher ──────────────────────────────────────────────────

// InMemoryPublisher is a simple in-memory implementation of Publisher that
// uses goroutines with time.AfterFunc.
//
// Suitable for single-instance deployments and development. For production
// multi-instance deployments, replace with AsynqPublisher or a persistent
// message broker implementation.
//
// Caveats:
//   - Delayed messages are lost if the process crashes.
//   - No persistence, no retry, no distributed coordination.
type InMemoryPublisher struct {
	handler HandlerFunc
}

// NewInMemoryPublisher creates a delayed publisher that calls the given
// handler function when a delayed message fires.
func NewInMemoryPublisher(handler HandlerFunc) *InMemoryPublisher {
	return &InMemoryPublisher{handler: handler}
}

// PublishDelayed schedules a message to be delivered after the given delay
// using an in-memory timer. The message is lost if the process crashes.
func (p *InMemoryPublisher) PublishDelayed(
	_ context.Context, topic string, payload []byte, delay time.Duration,
) error {
	// Copy payload to avoid data race (caller may reuse the slice)
	data := make([]byte, len(payload))
	copy(data, payload)

	time.AfterFunc(delay, func() {
		log.Printf("[delayed] firing event: topic=%s", topic)
		if p.handler != nil {
			p.handler(topic, data)
		}
	})

	log.Printf("[delayed] scheduled: topic=%s delay=%v", topic, delay)
	return nil
}
