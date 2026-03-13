package domain

import (
	"testing"
	"time"
)

func newTestVPSConfig(t *testing.T) VPSConfig {
	t.Helper()
	cfg, err := NewVPSConfig("web-01", "vps-starter", "us-east-1", "ubuntu-22.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected error creating VPSConfig: %v", err)
	}
	return cfg
}

func TestOrderLifecycle_ActivateAndSuspend(t *testing.T) {
	cfg := newTestVPSConfig(t)
	order, err := NewOrder("ord-1", "cust-1", "prod-1", "inv-1", "monthly", cfg, "USD", 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status() != OrderStatusPending {
		t.Fatalf("expected pending, got %s", order.Status())
	}

	now := time.Now()
	if err := order.Activate(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status() != OrderStatusActive {
		t.Fatalf("expected active, got %s", order.Status())
	}

	if err := order.Suspend(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status() != OrderStatusSuspended {
		t.Fatalf("expected suspended, got %s", order.Status())
	}

	if err := order.Unsuspend(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status() != OrderStatusActive {
		t.Fatalf("expected active after unsuspend, got %s", order.Status())
	}
}

func TestOrderLifecycle_Cancel(t *testing.T) {
	cfg := newTestVPSConfig(t)
	order, err := NewOrder("ord-2", "cust-1", "prod-2", "inv-2", "monthly", cfg, "USD", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now := time.Now()
	if err := order.Cancel("no longer needed", now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status() != OrderStatusCancelled {
		t.Fatalf("expected cancelled, got %s", order.Status())
	}
	if order.CancelReason() != "no longer needed" {
		t.Fatalf("expected cancel reason, got %s", order.CancelReason())
	}
}

func TestOrderLifecycle_Terminate(t *testing.T) {
	cfg := newTestVPSConfig(t)
	order, err := NewOrder("ord-3", "cust-1", "prod-3", "inv-3", "monthly", cfg, "USD", 800)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	now := time.Now()
	_ = order.Activate(now)
	if err := order.Terminate(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status() != OrderStatusTerminated {
		t.Fatalf("expected terminated, got %s", order.Status())
	}
}

func TestOrder_CannotActivateNonPending(t *testing.T) {
	cfg := newTestVPSConfig(t)
	order, _ := NewOrder("ord-4", "cust-1", "prod-4", "inv-4", "monthly", cfg, "USD", 500)
	now := time.Now()
	_ = order.Activate(now)

	if err := order.Activate(now); err == nil {
		t.Fatal("expected error activating an already active order")
	}
}

func TestOrder_CancelRequiresReason(t *testing.T) {
	cfg := newTestVPSConfig(t)
	order, _ := NewOrder("ord-5", "cust-1", "prod-5", "inv-5", "monthly", cfg, "USD", 500)

	if err := order.Cancel("", time.Now()); err == nil {
		t.Fatal("expected error when cancel reason is empty")
	}
}

func TestVPSConfig_Validation(t *testing.T) {
	_, err := NewVPSConfig("", "plan", "region", "os", 1, 1024, 20)
	if err == nil {
		t.Fatal("expected error for empty hostname")
	}

	_, err = NewVPSConfig("host", "plan", "region", "os", 0, 1024, 20)
	if err == nil {
		t.Fatal("expected error for zero cpu")
	}
}
