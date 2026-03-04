// Package eventbus provides a lightweight, in-process synchronous event bus
// for publishing and subscribing to domain events across bounded contexts.
//
// Architecture note: For a modular monolith, a synchronous in-process bus is
// the simplest correct choice. It preserves transactional consistency (all
// handlers run in the same goroutine/transaction boundary) and avoids the
// operational overhead of an external broker. When the system needs to scale
// to separate services, swap this for NATS / RabbitMQ / Kafka.
package eventbus

import "sync"

// Event is the marker interface every domain event must satisfy.
type Event interface {
	EventName() string
}

// Handler is a callback invoked when a matching event is published.
type Handler func(event Event)

// EventBus dispatches domain events to registered subscribers.
type EventBus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

// New creates a ready-to-use EventBus.
func New() *EventBus {
	return &EventBus{handlers: make(map[string][]Handler)}
}

// Subscribe registers a handler for a specific event name.
func (b *EventBus) Subscribe(eventName string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventName] = append(b.handlers[eventName], h)
}

// Publish dispatches an event to all registered handlers synchronously.
// Handlers are called in the order they were registered.
func (b *EventBus) Publish(event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, h := range b.handlers[event.EventName()] {
		h(event)
	}
}
