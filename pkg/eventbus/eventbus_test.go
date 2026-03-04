package eventbus

import "testing"

type testEvent struct{ msg string }

func (e testEvent) EventName() string { return "test.event" }

func TestEventBus_PublishSubscribe(t *testing.T) {
	bus := New()
	var received []string

	bus.Subscribe("test.event", func(evt Event) {
		e := evt.(testEvent)
		received = append(received, e.msg)
	})
	bus.Subscribe("test.event", func(evt Event) {
		e := evt.(testEvent)
		received = append(received, "handler2:"+e.msg)
	})

	bus.Publish(testEvent{msg: "hello"})

	if len(received) != 2 {
		t.Fatalf("expected 2 handlers called, got %d", len(received))
	}
	if received[0] != "hello" {
		t.Fatalf("expected 'hello', got %s", received[0])
	}
	if received[1] != "handler2:hello" {
		t.Fatalf("expected 'handler2:hello', got %s", received[1])
	}
}

func TestEventBus_NoSubscribers(t *testing.T) {
	bus := New()
	// Should not panic
	bus.Publish(testEvent{msg: "nobody listening"})
}

func TestEventBus_DifferentEvents(t *testing.T) {
	bus := New()
	called := false

	bus.Subscribe("other.event", func(evt Event) {
		called = true
	})

	bus.Publish(testEvent{msg: "should not trigger"})
	if called {
		t.Fatal("handler for different event name should not be called")
	}
}
