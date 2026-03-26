package adaptive

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// ── Test types ──────────────────────────────────────────────────────────────

type testRequest struct {
	ID string
}

type testResult struct {
	ID     string
	Mode   string // "sync" or "async"
	Status int
}

// mockSyncProcessor always returns sync results.
type mockSyncProcessor struct {
	callCount int64
}

func (p *mockSyncProcessor) Process(ctx context.Context, req testRequest) (*testResult, error) {
	atomic.AddInt64(&p.callCount, 1)
	return &testResult{ID: req.ID, Mode: "sync", Status: 200}, nil
}

// mockAsyncProcessor always returns async results.
type mockAsyncProcessor struct {
	callCount int64
}

func (p *mockAsyncProcessor) Process(ctx context.Context, req testRequest) (*testResult, error) {
	atomic.AddInt64(&p.callCount, 1)
	return &testResult{ID: req.ID, Mode: "async", Status: 202}, nil
}

// ── Tests ───────────────────────────────────────────────────────────────────

func TestDispatcher_RoutesToSyncUnderThreshold(t *testing.T) {
	syncProc := &mockSyncProcessor{}
	asyncProc := &mockAsyncProcessor{}
	monitor := NewSlidingWindowQPSMonitor(10)
	dispatcher := NewDispatcher[testRequest, *testResult](syncProc, asyncProc, monitor, 1000)

	result, err := dispatcher.Dispatch(context.Background(), testRequest{ID: "order-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != "sync" {
		t.Fatalf("expected sync mode, got %s", result.Mode)
	}
	if result.Status != 200 {
		t.Fatalf("expected status 200, got %d", result.Status)
	}
	if syncProc.callCount != 1 {
		t.Fatalf("expected 1 sync call, got %d", syncProc.callCount)
	}
	if asyncProc.callCount != 0 {
		t.Fatalf("expected 0 async calls, got %d", asyncProc.callCount)
	}
}

func TestDispatcher_RoutesToAsyncAboveThreshold(t *testing.T) {
	syncProc := &mockSyncProcessor{}
	asyncProc := &mockAsyncProcessor{}
	monitor := NewSlidingWindowQPSMonitor(1) // 1-second window for quick testing
	dispatcher := NewDispatcher[testRequest, *testResult](syncProc, asyncProc, monitor, 5)

	// Fire enough requests to exceed threshold of 5
	// With windowSize=1, all requests in the same second count
	for i := 0; i < 10; i++ {
		monitor.Record()
	}

	result, err := dispatcher.Dispatch(context.Background(), testRequest{ID: "order-high"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != "async" {
		t.Fatalf("expected async mode, got %s", result.Mode)
	}
	if result.Status != 202 {
		t.Fatalf("expected status 202, got %d", result.Status)
	}
}

func TestDispatcher_SetThreshold(t *testing.T) {
	syncProc := &mockSyncProcessor{}
	asyncProc := &mockAsyncProcessor{}
	monitor := NewSlidingWindowQPSMonitor(10)
	dispatcher := NewDispatcher[testRequest, *testResult](syncProc, asyncProc, monitor, 500)

	if dispatcher.GetThreshold() != 500 {
		t.Fatalf("expected threshold 500, got %d", dispatcher.GetThreshold())
	}

	dispatcher.SetThreshold(100)
	if dispatcher.GetThreshold() != 100 {
		t.Fatalf("expected threshold 100 after set, got %d", dispatcher.GetThreshold())
	}
}

func TestDispatcher_DefaultThreshold(t *testing.T) {
	syncProc := &mockSyncProcessor{}
	asyncProc := &mockAsyncProcessor{}
	monitor := NewSlidingWindowQPSMonitor(10)
	dispatcher := NewDispatcher[testRequest, *testResult](syncProc, asyncProc, monitor, 0) // 0 → default 500

	if dispatcher.GetThreshold() != 500 {
		t.Fatalf("expected default threshold 500, got %d", dispatcher.GetThreshold())
	}
}

func TestDispatcher_ConcurrentSafety(t *testing.T) {
	syncProc := &mockSyncProcessor{}
	asyncProc := &mockAsyncProcessor{}
	monitor := NewSlidingWindowQPSMonitor(10)
	dispatcher := NewDispatcher[testRequest, *testResult](syncProc, asyncProc, monitor, 500)

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := dispatcher.Dispatch(context.Background(), testRequest{ID: fmt.Sprintf("order-%d", idx)})
			if err != nil {
				errors <- err
			}
		}(i)
	}
	wg.Wait()
	close(errors)

	for err := range errors {
		t.Fatalf("concurrent dispatch error: %v", err)
	}

	totalCalls := syncProc.callCount + asyncProc.callCount
	if totalCalls != 100 {
		t.Fatalf("expected 100 total calls, got %d (sync=%d async=%d)",
			totalCalls, syncProc.callCount, asyncProc.callCount)
	}
}

// ── QPSMonitor Tests ────────────────────────────────────────────────────────

func TestQPSMonitor_Record(t *testing.T) {
	m := NewSlidingWindowQPSMonitor(10)

	m.Record()
	m.Record()
	m.Record()

	stats := m.Stats()
	if stats.TotalRequests != 3 {
		t.Fatalf("expected 3 total requests, got %d", stats.TotalRequests)
	}
}

func TestQPSMonitor_DefaultWindow(t *testing.T) {
	m := NewSlidingWindowQPSMonitor(0) // should default to 10
	if m.windowSize != 10 {
		t.Fatalf("expected default window 10, got %d", m.windowSize)
	}
}

func TestQPSMonitor_ConcurrentRecords(t *testing.T) {
	m := NewSlidingWindowQPSMonitor(10)

	var wg sync.WaitGroup
	wg.Add(1000)
	for i := 0; i < 1000; i++ {
		go func() {
			defer wg.Done()
			m.Record()
		}()
	}
	wg.Wait()

	stats := m.Stats()
	if stats.TotalRequests != 1000 {
		t.Fatalf("expected 1000 total requests, got %d", stats.TotalRequests)
	}
}
