// Package eventbus — AsyncEventBus extension.
//
// AsyncEventBus decouples event publishers from event handlers by processing
// events through a bounded channel + worker pool. This prevents slow handlers
// (e.g. provisioning, external API calls) from blocking the publishing
// goroutine, dramatically improving throughput under high concurrency.
//
// Architecture:
//
//	Publisher → AsyncEventBus.Publish()
//	              ↓ (non-blocking enqueue)
//	         [ bounded channel ]
//	              ↓
//	         worker-1 → handler(event)
//	         worker-2 → handler(event)
//	         worker-N → handler(event)
//
// When the queue is full, events are dropped with a warning log. This is
// a deliberate back-pressure design: it's better to drop a non-critical
// event than to block the entire purchase flow.
//
// Usage:
//
//	bus := eventbus.NewAsyncEventBus(4096, 8) // 4096 queue, 8 workers
//	bus.Subscribe("product.purchased", myHandler)
//	bus.Start()
//	defer bus.Stop()
//	bus.Publish(event)
package eventbus

import (
	"log"
	"sync"
)

// AsyncEventBus processes domain events asynchronously through a bounded
// worker pool. It wraps a synchronous EventBus for handler registration
// and dispatching, adding non-blocking enqueue + concurrent processing.
type AsyncEventBus struct {
	inner   *EventBus       // synchronous handler registry & dispatch
	queue   chan Event       // bounded event queue
	workers int             // number of worker goroutines
	wg      sync.WaitGroup  // tracks active workers for graceful shutdown
	once    sync.Once       // ensures Stop() is idempotent
}

// NewAsyncEventBus creates an asynchronous event bus.
//
// Parameters:
//   - queueSize: capacity of the internal channel (e.g. 4096).
//     Larger = more burst tolerance, more memory.
//   - workers: number of goroutines consuming from the queue (e.g. 4-16).
//     More workers = higher handler throughput, but more goroutine overhead.
//
// The bus must be started with Start() before publishing events.
func NewAsyncEventBus(queueSize, workers int) *AsyncEventBus {
	if queueSize <= 0 {
		queueSize = 4096
	}
	if workers <= 0 {
		workers = 4
	}
	return &AsyncEventBus{
		inner:   New(),
		queue:   make(chan Event, queueSize),
		workers: workers,
	}
}

// Subscribe registers a handler for a specific event name.
// Thread-safe; can be called before or after Start().
func (b *AsyncEventBus) Subscribe(eventName string, h Handler) {
	b.inner.Subscribe(eventName, h)
}

// Publish enqueues an event for asynchronous processing.
// This method is non-blocking: if the queue is full, the event is dropped
// with a warning log (back-pressure design).
//
// This is safe to call from any goroutine.
func (b *AsyncEventBus) Publish(event Event) {
	select {
	case b.queue <- event:
		// Enqueued successfully
	default:
		log.Printf("[async-eventbus] WARNING: queue full (%d), dropping event %s",
			cap(b.queue), event.EventName())
	}
}

// Start launches the worker goroutines. Must be called before Publish().
func (b *AsyncEventBus) Start() {
	for i := 0; i < b.workers; i++ {
		b.wg.Add(1)
		go b.worker(i)
	}
	log.Printf("[async-eventbus] started %d workers, queue capacity=%d", b.workers, cap(b.queue))
}

// Stop gracefully shuts down the event bus:
// 1. Closes the queue channel (no new events accepted)
// 2. Workers drain remaining events
// 3. Blocks until all workers have exited
//
// Safe to call multiple times (idempotent via sync.Once).
func (b *AsyncEventBus) Stop() {
	b.once.Do(func() {
		close(b.queue)
		b.wg.Wait()
		log.Printf("[async-eventbus] all workers stopped")
	})
}

// QueueLen returns the current number of events waiting in the queue.
// Useful for monitoring and back-pressure metrics.
func (b *AsyncEventBus) QueueLen() int {
	return len(b.queue)
}

// QueueCap returns the queue capacity.
func (b *AsyncEventBus) QueueCap() int {
	return cap(b.queue)
}

// worker is the background goroutine that consumes events from the queue
// and dispatches them to registered handlers via the synchronous EventBus.
func (b *AsyncEventBus) worker(id int) {
	defer b.wg.Done()

	for event := range b.queue {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[async-eventbus] worker %d: PANIC handling event %s: %v",
						id, event.EventName(), r)
				}
			}()
			b.inner.Publish(event)
		}()
	}
}

// AsyncEventBusStats holds metrics for monitoring the async event bus.
type AsyncEventBusStats struct {
	Workers   int `json:"workers"`
	QueueLen  int `json:"queue_len"`
	QueueCap  int `json:"queue_cap"`
}

// Stats returns a snapshot of the bus's current state.
func (b *AsyncEventBus) Stats() AsyncEventBusStats {
	return AsyncEventBusStats{
		Workers:  b.workers,
		QueueLen: len(b.queue),
		QueueCap: cap(b.queue),
	}
}
