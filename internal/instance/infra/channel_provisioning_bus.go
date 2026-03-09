package infra

import (
	"backend-core/internal/instance/domain"
	"context"
	"fmt"
	"log"
	"sync"
)

// ProvisionHandler is the callback invoked by the bus consumer for each
// provisioning request. Wire this to the actual provisioning logic (e.g.
// the ProvisioningEventHandler in the node domain, or any future handler).
type ProvisionHandler func(req domain.ProvisionRequest)

// ───────────────────────────────────────────────────────────────────────────
// ChannelProvisioningBus — in-memory buffered channel implementation
// ───────────────────────────────────────────────────────────────────────────

// ChannelProvisioningBus is an in-memory message queue backed by a Go channel.
// It buffers provisioning requests and processes them sequentially through a
// configurable number of worker goroutines (default: 1). This naturally
// throttles highly concurrent provisioning bursts without requiring an
// external broker.
//
// To swap to RabbitMQ or NATS in the future, implement ProvisioningBus and
// inject the alternative implementation — no callers need to change.
type ChannelProvisioningBus struct {
	ch      chan domain.ProvisionRequest
	handler ProvisionHandler
	workers int
	wg      sync.WaitGroup
}

// ChannelBusOption configures a ChannelProvisioningBus.
type ChannelBusOption func(*ChannelProvisioningBus)

// WithBufferSize sets the channel buffer capacity (default: 256).
func WithBufferSize(n int) ChannelBusOption {
	return func(b *ChannelProvisioningBus) {
		b.ch = make(chan domain.ProvisionRequest, n)
	}
}

// WithWorkers sets the number of concurrent consumer goroutines (default: 1).
func WithWorkers(n int) ChannelBusOption {
	return func(b *ChannelProvisioningBus) {
		if n > 0 {
			b.workers = n
		}
	}
}

// NewChannelProvisioningBus creates a channel-based provisioning bus.
// The handler is called for every dispatched request in the consumer
// goroutine(s).
func NewChannelProvisioningBus(handler ProvisionHandler, opts ...ChannelBusOption) *ChannelProvisioningBus {
	b := &ChannelProvisioningBus{
		ch:      make(chan domain.ProvisionRequest, 256),
		handler: handler,
		workers: 1,
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Dispatch enqueues a provisioning request into the channel buffer.
// Returns an error only if the buffer is full (back-pressure).
func (b *ChannelProvisioningBus) Dispatch(req domain.ProvisionRequest) error {
	select {
	case b.ch <- req:
		log.Printf("[ChannelProvisioningBus] dispatched: instance=%s order=%s", req.InstanceID, req.OrderID)
		return nil
	default:
		return fmt.Errorf("provisioning bus: buffer full, cannot dispatch instance=%s", req.InstanceID)
	}
}

// Start launches the worker goroutines that consume from the channel.
// Call this once during application bootstrap. The workers run until the
// context is cancelled, at which point they drain remaining items and exit.
func (b *ChannelProvisioningBus) Start(ctx context.Context) {
	for i := 0; i < b.workers; i++ {
		b.wg.Add(1)
		go b.worker(ctx, i)
	}
	log.Printf("[ChannelProvisioningBus] started %d worker(s), buffer=%d", b.workers, cap(b.ch))
}

// Stop signals the workers to finish and waits for them to drain.
func (b *ChannelProvisioningBus) Stop() {
	close(b.ch)
	b.wg.Wait()
	log.Printf("[ChannelProvisioningBus] all workers stopped")
}

func (b *ChannelProvisioningBus) worker(ctx context.Context, id int) {
	defer b.wg.Done()
	for {
		select {
		case req, ok := <-b.ch:
			if !ok {
				log.Printf("[ChannelProvisioningBus] worker %d: channel closed, exiting", id)
				return
			}
			b.safeHandle(req, id)
		case <-ctx.Done():
			// Drain remaining items before exiting
			for req := range b.ch {
				b.safeHandle(req, id)
			}
			log.Printf("[ChannelProvisioningBus] worker %d: context done, exiting", id)
			return
		}
	}
}

func (b *ChannelProvisioningBus) safeHandle(req domain.ProvisionRequest, workerID int) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ChannelProvisioningBus] worker %d: PANIC handling instance=%s: %v", workerID, req.InstanceID, r)
		}
	}()
	log.Printf("[ChannelProvisioningBus] worker %d: processing instance=%s order=%s", workerID, req.InstanceID, req.OrderID)
	b.handler(req)
}

// ───────────────────────────────────────────────────────────────────────────
// NoopProvisioningBus — test / stub implementation
// ───────────────────────────────────────────────────────────────────────────

// NoopProvisioningBus silently discards all dispatched requests.
// Useful for unit tests or when provisioning should be disabled.
type NoopProvisioningBus struct{}

func NewNoopProvisioningBus() *NoopProvisioningBus { return &NoopProvisioningBus{} }

func (b *NoopProvisioningBus) Dispatch(_ domain.ProvisionRequest) error {
	return nil
}
