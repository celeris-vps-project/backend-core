package delayed

import "context"

// Consumer abstracts a message queue consumer/worker that pulls delayed
// messages from a broker and dispatches them to registered handlers.
//
// The Consumer is the "read side" counterpart of the Publisher interface.
// Together they form a complete async messaging abstraction:
//
//	Publisher  → enqueue (with optional delay)
//	Consumer   → dequeue & process
//
// Implementations:
//   - InMemoryConsumer: no-op (InMemoryPublisher fires handlers inline via Router)
//   - AsynqConsumer:    wraps asynq.Server to process Redis-backed tasks
//   - (future) KafkaConsumer, RabbitMQConsumer, NATSConsumer
type Consumer interface {
	// Start begins consuming messages from the underlying broker.
	// It blocks until the context is cancelled or a fatal error occurs.
	Start(ctx context.Context) error

	// Close gracefully shuts down the consumer, waiting for in-flight
	// messages to finish processing before returning.
	Close() error
}

// ── InMemoryConsumer ───────────────────────────────────────────────────

// InMemoryConsumer is a no-op Consumer implementation.
//
// When using InMemoryPublisher, the handler callback is invoked directly
// by time.AfterFunc inside the publisher — there is no separate consumer
// process. This struct exists so callers can program against the Consumer
// interface uniformly without nil-checks.
type InMemoryConsumer struct{}

// NewInMemoryConsumer returns a no-op consumer.
func NewInMemoryConsumer() *InMemoryConsumer { return &InMemoryConsumer{} }

// Start blocks until the context is done (no-op for in-memory).
func (c *InMemoryConsumer) Start(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Close is a no-op for in-memory.
func (c *InMemoryConsumer) Close() error { return nil }
