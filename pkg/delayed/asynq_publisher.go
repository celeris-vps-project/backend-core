package delayed

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hibiken/asynq"
)

// ── AsynqPublisher ─────────────────────────────────────────────────────

// AsynqPublisher is a Redis-backed implementation of Publisher using the
// Asynq library. Tasks are persisted in Redis and processed by an
// AsynqConsumer worker, providing:
//
//   - Persistence: tasks survive process restarts.
//   - Retry: automatic retry with exponential backoff on failure.
//   - Distributed: multiple instances can enqueue; one or more consume.
//   - Delay: native support for scheduled / delayed tasks.
//
// For production multi-instance deployments, use AsynqPublisher +
// AsynqConsumer instead of InMemoryPublisher.
//
// Usage:
//
//	pub := delayed.NewAsynqPublisher(asynq.RedisClientOpt{Addr: "localhost:6379"})
//	defer pub.Close()
//	pub.PublishDelayed(ctx, "invoice.check_timeout", payload, 30*time.Minute)
type AsynqPublisher struct {
	client *asynq.Client
}

// AsynqPublisherOption configures an AsynqPublisher.
type AsynqPublisherOption func(*AsynqPublisher)

// NewAsynqPublisher creates a Publisher backed by Redis via Asynq.
// The redisOpt is typically asynq.RedisClientOpt{Addr: "host:port"}.
func NewAsynqPublisher(redisOpt asynq.RedisConnOpt, opts ...AsynqPublisherOption) *AsynqPublisher {
	p := &AsynqPublisher{
		client: asynq.NewClient(redisOpt),
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// PublishDelayed enqueues a task to Redis with the given delay.
// The topic is used as the Asynq task type; the payload is stored as-is.
//
// If delay == 0 the task is processed immediately by the next available worker.
func (p *AsynqPublisher) PublishDelayed(
	ctx context.Context, topic string, payload []byte, delay time.Duration,
) error {
	task := asynq.NewTask(topic, payload)

	var opts []asynq.Option
	if delay > 0 {
		opts = append(opts, asynq.ProcessIn(delay))
	}

	info, err := p.client.EnqueueContext(ctx, task, opts...)
	if err != nil {
		return fmt.Errorf("asynq publish %s: %w", topic, err)
	}
	log.Printf("[delayed-asynq] enqueued: topic=%s id=%s delay=%v queue=%s",
		topic, info.ID, delay, info.Queue)
	return nil
}

// Close releases the underlying Redis connection.
func (p *AsynqPublisher) Close() error {
	return p.client.Close()
}
