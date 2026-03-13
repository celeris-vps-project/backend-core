package delayed

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestInMemoryPublisher_FiresAfterDelay(t *testing.T) {
	var mu sync.Mutex
	var received []string

	handler := func(topic string, payload []byte) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, topic+":"+string(payload))
	}

	pub := NewInMemoryPublisher(handler)
	if err := pub.PublishDelayed(context.Background(), "test.topic", []byte("hello"), 50*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not have fired yet
	mu.Lock()
	if len(received) != 0 {
		t.Fatalf("expected no messages yet, got %d", len(received))
	}
	mu.Unlock()

	// Wait for the delay
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 message, got %d", len(received))
	}
	if received[0] != "test.topic:hello" {
		t.Fatalf("expected 'test.topic:hello', got %q", received[0])
	}
}

func TestRouter_DispatchesToCorrectHandler(t *testing.T) {
	var mu sync.Mutex
	results := map[string]string{}

	router := NewRouter()
	router.Handle("topic.a", func(topic string, payload []byte) {
		mu.Lock()
		defer mu.Unlock()
		results["a"] = string(payload)
	})
	router.Handle("topic.b", func(topic string, payload []byte) {
		mu.Lock()
		defer mu.Unlock()
		results["b"] = string(payload)
	})

	router.Dispatch("topic.a", []byte("payload-a"))
	router.Dispatch("topic.b", []byte("payload-b"))
	router.Dispatch("topic.unknown", []byte("should-be-discarded"))

	mu.Lock()
	defer mu.Unlock()
	if results["a"] != "payload-a" {
		t.Fatalf("expected 'payload-a', got %q", results["a"])
	}
	if results["b"] != "payload-b" {
		t.Fatalf("expected 'payload-b', got %q", results["b"])
	}
	if _, ok := results["unknown"]; ok {
		t.Fatalf("unexpected handler call for unknown topic")
	}
}

func TestRouter_IntegrationWithPublisher(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	var gotTopic string
	var gotPayload string

	router := NewRouter()
	router.Handle("invoice.timeout", func(topic string, payload []byte) {
		gotTopic = topic
		gotPayload = string(payload)
		wg.Done()
	})

	pub := NewInMemoryPublisher(router.Dispatch)
	if err := pub.PublishDelayed(context.Background(), "invoice.timeout", []byte(`{"id":"inv-1"}`), 30*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wg.Wait()

	if gotTopic != "invoice.timeout" {
		t.Fatalf("expected topic 'invoice.timeout', got %q", gotTopic)
	}
	if gotPayload != `{"id":"inv-1"}` {
		t.Fatalf("expected payload '{\"id\":\"inv-1\"}', got %q", gotPayload)
	}
}

// ── Consumer interface tests ───────────────────────────────────────────

func TestInMemoryConsumer_StartBlocksUntilCancel(t *testing.T) {
	consumer := NewInMemoryConsumer()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- consumer.Start(ctx)
	}()

	// Consumer should be blocking
	select {
	case <-done:
		t.Fatal("InMemoryConsumer.Start returned before context cancellation")
	case <-time.After(50 * time.Millisecond):
		// expected — still blocking
	}

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("InMemoryConsumer.Start did not return after context cancellation")
	}
}

func TestInMemoryConsumer_CloseIsNoop(t *testing.T) {
	consumer := NewInMemoryConsumer()
	if err := consumer.Close(); err != nil {
		t.Fatalf("expected nil error from Close, got %v", err)
	}
}

// ── Publisher interface compliance ─────────────────────────────────────

func TestInMemoryPublisher_ImplementsPublisher(t *testing.T) {
	var _ Publisher = (*InMemoryPublisher)(nil)
}

func TestAsynqPublisher_ImplementsPublisher(t *testing.T) {
	var _ Publisher = (*AsynqPublisher)(nil)
}

// ── Consumer interface compliance ──────────────────────────────────────

func TestInMemoryConsumer_ImplementsConsumer(t *testing.T) {
	var _ Consumer = (*InMemoryConsumer)(nil)
}

func TestAsynqConsumer_ImplementsConsumer(t *testing.T) {
	var _ Consumer = (*AsynqConsumer)(nil)
}

// ── Boot confirmation topic integration test ──────────────────────────

func TestRouter_BootConfirmationTopic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	var gotTopic string
	var gotPayload string

	router := NewRouter()
	router.Handle("provision.confirm_boot", func(topic string, payload []byte) {
		gotTopic = topic
		gotPayload = string(payload)
		wg.Done()
	})

	pub := NewInMemoryPublisher(router.Dispatch)
	payload := `{"instance_id":"inst-1","task_id":"task-1","node_id":"node-1"}`
	if err := pub.PublishDelayed(context.Background(), "provision.confirm_boot", []byte(payload), 30*time.Millisecond); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wg.Wait()

	if gotTopic != "provision.confirm_boot" {
		t.Fatalf("expected topic 'provision.confirm_boot', got %q", gotTopic)
	}
	if gotPayload != payload {
		t.Fatalf("expected payload %q, got %q", payload, gotPayload)
	}
}
