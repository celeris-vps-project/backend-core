package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := New("test", 3, 2, 100*time.Millisecond)

	// Initially closed
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed, got %s", cb.State())
	}

	// 2 failures: still closed
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after 2 failures, got %s", cb.State())
	}

	// 3rd failure: should trip to open
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen after 3 failures, got %s", cb.State())
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := New("test", 2, 1, 50*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %s", cb.State())
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen after timeout, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClosedOnSuccess(t *testing.T) {
	cb := New("test", 2, 2, 50*time.Millisecond)

	// Trip to open
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen, got %s", cb.State())
	}

	// 1 success: still half-open
	cb.RecordSuccess()
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen after 1 success, got %s", cb.State())
	}

	// 2nd success: should close
	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after 2 successes, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToOpenOnFailure(t *testing.T) {
	cb := New("test", 2, 2, 50*time.Millisecond)

	// Trip to open
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen, got %s", cb.State())
	}

	// Failure in half-open → re-opens
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen after failure in half-open, got %s", cb.State())
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := New("test", 3, 1, 100*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // resets failure count
	cb.RecordFailure()
	cb.RecordFailure()

	// Should still be closed (2 consecutive, not 3)
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed, got %s", cb.State())
	}
}

func TestCircuitBreaker_Allow(t *testing.T) {
	cb := New("test", 2, 1, 100*time.Millisecond)

	if !cb.Allow() {
		t.Fatal("expected Allow=true when closed")
	}

	cb.RecordFailure()
	cb.RecordFailure()

	if cb.Allow() {
		t.Fatal("expected Allow=false when open")
	}
}

func TestExecute_Success(t *testing.T) {
	cb := New("test", 3, 1, 100*time.Millisecond)

	result, err := Execute(cb, func() (string, error) {
		return "hello", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Fatalf("expected 'hello', got %q", result)
	}
}

func TestExecute_Failure(t *testing.T) {
	cb := New("test", 3, 1, 100*time.Millisecond)

	testErr := errors.New("service down")
	_, err := Execute(cb, func() (string, error) {
		return "", testErr
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, testErr) {
		t.Fatalf("expected testErr, got %v", err)
	}
}

func TestExecute_CircuitOpen(t *testing.T) {
	cb := New("test", 2, 1, 1*time.Second)

	testErr := errors.New("fail")
	// Trip the breaker
	Execute(cb, func() (string, error) { return "", testErr })
	Execute(cb, func() (string, error) { return "", testErr })

	// Should be rejected
	_, err := Execute(cb, func() (string, error) {
		t.Fatal("should not be called when circuit is open")
		return "", nil
	})
	if err == nil {
		t.Fatal("expected circuit open error")
	}
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestExecuteNoResult_Success(t *testing.T) {
	cb := New("test", 3, 1, 100*time.Millisecond)

	err := ExecuteNoResult(cb, func() error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecuteNoResult_CircuitOpen(t *testing.T) {
	cb := New("test", 2, 1, 1*time.Second)

	// Trip the breaker
	ExecuteNoResult(cb, func() error { return errors.New("fail") })
	ExecuteNoResult(cb, func() error { return errors.New("fail") })

	err := ExecuteNoResult(cb, func() error {
		t.Fatal("should not be called")
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestStats(t *testing.T) {
	cb := New("test-stats", 2, 1, 100*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()

	stats := cb.Stats()
	if stats.Name != "test-stats" {
		t.Fatalf("expected name 'test-stats', got %q", stats.Name)
	}
	if stats.State != "open" {
		t.Fatalf("expected state 'open', got %q", stats.State)
	}
	if stats.TotalTrips != 1 {
		t.Fatalf("expected 1 trip, got %d", stats.TotalTrips)
	}
}
