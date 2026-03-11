package infra

import (
	checkoutApp "backend-core/internal/checkout/app"
	"backend-core/internal/checkout/domain"
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// AsyncCheckoutProcessor handles purchases asynchronously under high load:
//
//  1. Generate a placeholder order ID
//  2. Enqueue the request into a buffered Go channel
//  3. Return HTTP 202 (Accepted) with queue position
//  4. Background workers consume the queue and call CheckoutAppService.Execute()
//
// This processor is activated by the adaptive dispatcher when QPS >= threshold.
// The key insight: the actual business logic (product slot consumption, order
// creation) is deferred to background workers, dramatically reducing response
// latency under high load.
type AsyncCheckoutProcessor struct {
	svc         *checkoutApp.CheckoutAppService
	statusStore *OrderStatusStore
	queue       chan asyncOrder
	workers     int
	wg          sync.WaitGroup
	queueID     int64 // atomic counter for queue position
}

// asyncOrder is the internal representation of a queued checkout request.
type asyncOrder struct {
	OrderID    string
	ProductID  string
	CustomerID string
	Hostname   string
	OS         string
	QueuePos   int
}

// AsyncCheckoutOption configures an AsyncCheckoutProcessor.
type AsyncCheckoutOption func(*AsyncCheckoutProcessor)

// WithQueueSize sets the channel buffer capacity (default: 4096).
func WithQueueSize(n int) AsyncCheckoutOption {
	return func(p *AsyncCheckoutProcessor) {
		p.queue = make(chan asyncOrder, n)
	}
}

// WithAsyncWorkers sets the number of background workers (default: 4).
func WithAsyncWorkers(n int) AsyncCheckoutOption {
	return func(p *AsyncCheckoutProcessor) {
		if n > 0 {
			p.workers = n
		}
	}
}

// NewAsyncCheckoutProcessor creates an async checkout processor.
// Call Start() to launch background workers before processing requests.
func NewAsyncCheckoutProcessor(
	svc *checkoutApp.CheckoutAppService,
	statusStore *OrderStatusStore,
	opts ...AsyncCheckoutOption,
) *AsyncCheckoutProcessor {
	p := &AsyncCheckoutProcessor{
		svc:         svc,
		statusStore: statusStore,
		queue:       make(chan asyncOrder, 4096),
		workers:     4,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Process enqueues the checkout request for async processing.
// Returns HTTP 202 with a placeholder order ID and queue position.
//
// Note: unlike the old flash-sale async processor, stock is NOT deducted
// here. The actual CheckoutAppService.Execute() handles everything
// (product slot consumption, order creation) in the background worker.
// This means there's a brief window where the slot count is optimistic,
// but for non-flash-sale scenarios this is acceptable.
func (p *AsyncCheckoutProcessor) Process(req domain.CheckoutRequest) (*domain.CheckoutResult, error) {
	if req.ProductID == "" || req.CustomerID == "" {
		return nil, fmt.Errorf("checkout_error: product_id and customer_id are required")
	}

	// Generate a temporary order ID for tracking (the real order ID will be
	// created by CheckoutAppService.Execute in the background worker)
	tempOrderID := uuid.New().String()
	pos := int(atomic.AddInt64(&p.queueID, 1))

	// Record initial status
	if p.statusStore != nil {
		p.statusStore.Set(tempOrderID, OrderStatus{
			OrderID:  tempOrderID,
			Status:   "queued",
			QueuePos: pos,
		})
	}

	// Enqueue for background processing
	order := asyncOrder{
		OrderID:    tempOrderID,
		ProductID:  req.ProductID,
		CustomerID: req.CustomerID,
		Hostname:   req.Hostname,
		OS:         req.OS,
		QueuePos:   pos,
	}

	select {
	case p.queue <- order:
		log.Printf("[AsyncCheckout] QUEUED: order=%s pos=%d product=%s customer=%s",
			tempOrderID, pos, req.ProductID, req.CustomerID)
	default:
		// Queue full — reject
		return nil, fmt.Errorf("checkout_error: queue full, please try again later")
	}

	return &domain.CheckoutResult{
		HTTPStatus: 202,
		OrderID:    tempOrderID,
		Message:    "order queued for processing",
		QueuePos:   pos,
	}, nil
}

// Start launches background workers that consume from the queue.
func (p *AsyncCheckoutProcessor) Start(ctx context.Context) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}
	log.Printf("[AsyncCheckout] started %d workers, queue capacity=%d", p.workers, cap(p.queue))
}

// Stop signals workers to drain and exit.
func (p *AsyncCheckoutProcessor) Stop() {
	close(p.queue)
	p.wg.Wait()
	log.Printf("[AsyncCheckout] all workers stopped")
}

func (p *AsyncCheckoutProcessor) worker(ctx context.Context, id int) {
	defer p.wg.Done()
	for {
		select {
		case order, ok := <-p.queue:
			if !ok {
				return
			}
			p.processOrder(order, id)
		case <-ctx.Done():
			// Drain remaining orders before exiting
			for order := range p.queue {
				p.processOrder(order, id)
			}
			return
		}
	}
}

func (p *AsyncCheckoutProcessor) processOrder(order asyncOrder, workerID int) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[AsyncCheckout] worker %d: PANIC order=%s: %v", workerID, order.OrderID, r)
			if p.statusStore != nil {
				p.statusStore.Set(order.OrderID, OrderStatus{
					OrderID: order.OrderID,
					Status:  "failed",
					Message: fmt.Sprintf("internal error: %v", r),
				})
			}
		}
	}()

	// Update status to processing
	if p.statusStore != nil {
		p.statusStore.Set(order.OrderID, OrderStatus{
			OrderID: order.OrderID,
			Status:  "processing",
		})
	}

	// Execute the full checkout flow via the app service
	result, err := p.svc.Execute(domain.CheckoutRequest{
		ProductID:  order.ProductID,
		CustomerID: order.CustomerID,
		Hostname:   order.Hostname,
		OS:         order.OS,
	})

	if err != nil {
		log.Printf("[AsyncCheckout] worker %d: FAILED order=%s: %v", workerID, order.OrderID, err)
		if p.statusStore != nil {
			p.statusStore.Set(order.OrderID, OrderStatus{
				OrderID: order.OrderID,
				Status:  "failed",
				Message: err.Error(),
			})
		}
		return
	}

	// Success — update with the real order ID from the app service
	if p.statusStore != nil {
		p.statusStore.Set(order.OrderID, OrderStatus{
			OrderID: result.OrderID, // real order ID from ordering domain
			Status:  "completed",
			Message: result.Message,
		})
	}

	log.Printf("[AsyncCheckout] worker %d: COMPLETED temp=%s real_order=%s product=%s",
		workerID, order.OrderID, result.OrderID, order.ProductID)
}
