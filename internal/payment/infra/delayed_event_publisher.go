package infra

import (
	"context"
	"log"
	"time"
)

// InMemoryDelayedPublisher is a simple in-memory implementation of
// DelayedEventPublisher that uses goroutines with time.AfterFunc.
//
// This is suitable for single-instance deployments and development.
// For production multi-instance deployments, replace with an Asynq (Redis)
// or RabbitMQ-based implementation that persists delayed tasks.
type InMemoryDelayedPublisher struct {
	handler func(topic string, payload []byte)
}

// NewInMemoryDelayedPublisher creates a delayed publisher that calls the
// given handler function when a delayed message fires.
func NewInMemoryDelayedPublisher(handler func(topic string, payload []byte)) *InMemoryDelayedPublisher {
	return &InMemoryDelayedPublisher{handler: handler}
}

// PublishDelayed schedules a message to be delivered after the given delay
// using an in-memory timer. The message is lost if the process crashes.
func (p *InMemoryDelayedPublisher) PublishDelayed(
	_ context.Context, topic string, payload []byte, delay time.Duration,
) error {
	// Copy payload to avoid data race (caller may reuse the slice)
	data := make([]byte, len(payload))
	copy(data, payload)

	time.AfterFunc(delay, func() {
		log.Printf("[InMemoryDelayedPublisher] firing delayed event: topic=%s", topic)
		if p.handler != nil {
			p.handler(topic, data)
		}
	})

	log.Printf("[InMemoryDelayedPublisher] scheduled: topic=%s delay=%v", topic, delay)
	return nil
}
