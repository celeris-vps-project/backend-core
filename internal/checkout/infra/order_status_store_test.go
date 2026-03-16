package infra

import (
	"sync"
	"testing"
	"time"
)

func TestOrderStatusStore_GetSet(t *testing.T) {
	store := NewOrderStatusStore()

	// Initially empty
	_, ok := store.Get("order-1")
	if ok {
		t.Fatal("expected order-1 to not exist")
	}

	// Set and retrieve
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "queued", QueuePos: 1})
	st, ok := store.Get("order-1")
	if !ok {
		t.Fatal("expected order-1 to exist after Set")
	}
	if st.Status != "queued" || st.QueuePos != 1 {
		t.Fatalf("unexpected status: %+v", st)
	}

	// Overwrite
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "processing"})
	st, _ = store.Get("order-1")
	if st.Status != "processing" {
		t.Fatalf("expected status=processing, got %s", st.Status)
	}
}

func TestOrderStatusStore_SubscribeReceivesUpdates(t *testing.T) {
	store := NewOrderStatusStore()

	// Set initial status
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "queued"})

	// Subscribe
	ch := store.Subscribe("order-1")

	// Update status — subscriber should receive it
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "processing"})

	select {
	case st := <-ch:
		if st.Status != "processing" {
			t.Fatalf("expected status=processing, got %s", st.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive update within 1s")
	}

	// Second update
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "completed", Message: "done"})

	select {
	case st := <-ch:
		if st.Status != "completed" || st.Message != "done" {
			t.Fatalf("unexpected status: %+v", st)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive second update")
	}

	// Unsubscribe
	store.Unsubscribe("order-1", ch)

	// Channel should be closed after Unsubscribe
	_, open := <-ch
	if open {
		t.Fatal("expected channel to be closed after Unsubscribe")
	}
}

func TestOrderStatusStore_MultipleSubscribers(t *testing.T) {
	store := NewOrderStatusStore()
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "queued"})

	ch1 := store.Subscribe("order-1")
	ch2 := store.Subscribe("order-1")

	// Both subscribers should receive the update
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "processing"})

	for i, ch := range []chan OrderStatus{ch1, ch2} {
		select {
		case st := <-ch:
			if st.Status != "processing" {
				t.Fatalf("subscriber %d: expected processing, got %s", i, st.Status)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: did not receive update", i)
		}
	}

	// Unsubscribe ch1, ch2 should still work
	store.Unsubscribe("order-1", ch1)
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "completed"})

	select {
	case st := <-ch2:
		if st.Status != "completed" {
			t.Fatalf("ch2: expected completed, got %s", st.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("ch2 did not receive update after ch1 was unsubscribed")
	}

	store.Unsubscribe("order-1", ch2)
}

func TestOrderStatusStore_UnsubscribeCleanup(t *testing.T) {
	store := NewOrderStatusStore()
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "queued"})

	ch := store.Subscribe("order-1")
	store.Unsubscribe("order-1", ch)

	// After unsubscribing the only subscriber, the subscribers map entry
	// should be cleaned up
	store.mu.RLock()
	_, exists := store.subscribers["order-1"]
	store.mu.RUnlock()
	if exists {
		t.Fatal("expected subscribers map entry to be cleaned up")
	}
}

func TestOrderStatusStore_ConcurrentSetSubscribe(t *testing.T) {
	store := NewOrderStatusStore()
	store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "queued"})

	ch := store.Subscribe("order-1")
	var wg sync.WaitGroup

	// Writer goroutine: rapidly update status
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "processing", QueuePos: i})
		}
		store.Set("order-1", OrderStatus{OrderID: "order-1", Status: "completed"})
	}()

	// Reader goroutine: consume events with a timeout.
	// Note: the non-blocking fan-out may drop events when the channel is
	// full, so we use a timeout-based drain rather than waiting for a
	// specific terminal event. This matches real SSE usage where the
	// handler also has a timeout.
	wg.Add(1)
	received := 0
	go func() {
		defer wg.Done()
		timeout := time.After(3 * time.Second)
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
				received++
			case <-timeout:
				return
			}
		}
	}()

	wg.Wait()

	// Unsubscribe and verify
	store.Unsubscribe("order-1", ch)

	st, _ := store.Get("order-1")
	if st.Status != "completed" {
		t.Fatalf("final store status should be completed, got %s", st.Status)
	}
	if received == 0 {
		t.Fatal("reader should have received at least one event")
	}
	t.Logf("reader received %d/%d events (non-blocking fan-out may drop some)", received, 101)
}

func TestOrderStatusStore_DifferentOrders(t *testing.T) {
	store := NewOrderStatusStore()
	store.Set("order-A", OrderStatus{OrderID: "order-A", Status: "queued"})
	store.Set("order-B", OrderStatus{OrderID: "order-B", Status: "queued"})

	chA := store.Subscribe("order-A")
	chB := store.Subscribe("order-B")

	// Update order-A — only chA should receive
	store.Set("order-A", OrderStatus{OrderID: "order-A", Status: "completed"})

	select {
	case st := <-chA:
		if st.Status != "completed" {
			t.Fatalf("chA: expected completed, got %s", st.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("chA did not receive update")
	}

	// chB should NOT have received anything
	select {
	case st := <-chB:
		t.Fatalf("chB should not have received update, got %+v", st)
	case <-time.After(50 * time.Millisecond):
		// Good — no update for chB
	}

	store.Unsubscribe("order-A", chA)
	store.Unsubscribe("order-B", chB)
}
