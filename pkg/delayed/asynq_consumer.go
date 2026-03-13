package delayed

import (
	"context"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
)

// ── AsynqConsumer ──────────────────────────────────────────────────────

// AsynqConsumer implements the Consumer interface using an Asynq Server.
// It pulls tasks from Redis, matches them by topic (Asynq task type), and
// dispatches to the Router's registered HandlerFunc callbacks.
//
// Usage:
//
//	router := delayed.NewRouter()
//	router.Handle("invoice.check_timeout", invoiceWorker.HandlerFunc())
//	router.Handle("provision.confirm_boot", bootWorker.HandlerFunc())
//
//	consumer := delayed.NewAsynqConsumer(
//	    asynq.RedisClientOpt{Addr: "localhost:6379"},
//	    router,
//	)
//	go consumer.Start(ctx)   // blocks until ctx cancelled
//	defer consumer.Close()
type AsynqConsumer struct {
	server *asynq.Server
	mux    *asynq.ServeMux
	router *Router
}

// AsynqConsumerOption configures an AsynqConsumer.
type AsynqConsumerOption func(*asynq.Config)

// WithConcurrency sets the number of concurrent Asynq worker goroutines.
func WithConcurrency(n int) AsynqConsumerOption {
	return func(cfg *asynq.Config) {
		cfg.Concurrency = n
	}
}

// WithAsynqQueues sets the queue priority map for the Asynq server.
// For example: map[string]int{"default": 5, "critical": 10}
func WithAsynqQueues(queues map[string]int) AsynqConsumerOption {
	return func(cfg *asynq.Config) {
		cfg.Queues = queues
	}
}

// NewAsynqConsumer creates a Consumer backed by Redis via Asynq.
//
// The router provides the topic→handler mapping. All topics registered in
// the router are automatically wired to the Asynq ServeMux as task handlers.
// Topics registered after NewAsynqConsumer is called will NOT be picked up
// — register all handlers on the Router before creating the consumer.
func NewAsynqConsumer(redisOpt asynq.RedisConnOpt, router *Router, opts ...AsynqConsumerOption) *AsynqConsumer {
	cfg := asynq.Config{
		Concurrency: 10,
	}
	for _, o := range opts {
		o(&cfg)
	}

	srv := asynq.NewServer(redisOpt, cfg)
	mux := asynq.NewServeMux()

	// Bridge: for each topic in the Router, register an asynq handler that
	// calls the Router's Dispatch method (which in turn calls HandlerFunc).
	router.mu.RLock()
	for topic := range router.handlers {
		t := topic // capture
		mux.HandleFunc(t, func(ctx context.Context, task *asynq.Task) error {
			router.Dispatch(t, task.Payload())
			return nil
		})
		log.Printf("[delayed-asynq] registered asynq handler for topic %q", t)
	}
	router.mu.RUnlock()

	return &AsynqConsumer{
		server: srv,
		mux:    mux,
		router: router,
	}
}

// Start begins processing tasks from Redis. This method blocks until the
// context is cancelled or a fatal error occurs.
func (c *AsynqConsumer) Start(ctx context.Context) error {
	log.Printf("[delayed-asynq] starting consumer...")

	errCh := make(chan error, 1)
	go func() {
		if err := c.server.Run(c.mux); err != nil {
			errCh <- fmt.Errorf("asynq server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		c.server.Shutdown()
		return ctx.Err()
	}
}

// Close gracefully shuts down the Asynq server.
func (c *AsynqConsumer) Close() error {
	c.server.Shutdown()
	return nil
}
