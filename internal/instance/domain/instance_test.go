package domain

import (
	"testing"
	"time"
)

func TestInstanceLifecycle(t *testing.T) {
	inst, err := NewInstance("ins-1", "cust-1", "ord-1", "node-1", "web-01", "vps-starter", "ubuntu-22.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.Status() != InstanceStatusPending {
		t.Fatalf("expected pending, got %s", inst.Status())
	}

	now := time.Now()
	if err := inst.Start(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.Status() != InstanceStatusRunning {
		t.Fatalf("expected running, got %s", inst.Status())
	}

	if err := inst.Stop(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.Status() != InstanceStatusStopped {
		t.Fatalf("expected stopped, got %s", inst.Status())
	}

	if err := inst.Start(now); err != nil {
		t.Fatalf("unexpected error restarting: %v", err)
	}
	if inst.Status() != InstanceStatusRunning {
		t.Fatalf("expected running, got %s", inst.Status())
	}
}

func TestInstanceSuspendUnsuspend(t *testing.T) {
	inst, _ := NewInstance("ins-2", "cust-1", "ord-1", "node-1", "db-01", "vps-pro", "debian-12", 4, 8192, 100)
	now := time.Now()
	_ = inst.Start(now)

	if err := inst.Suspend(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.Status() != InstanceStatusSuspended {
		t.Fatalf("expected suspended, got %s", inst.Status())
	}

	if err := inst.Unsuspend(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.Status() != InstanceStatusRunning {
		t.Fatalf("expected running, got %s", inst.Status())
	}
}

func TestInstanceTerminate(t *testing.T) {
	inst, _ := NewInstance("ins-3", "cust-1", "ord-1", "node-1", "app-01", "vps-starter", "centos-9", 1, 1024, 20)
	now := time.Now()
	_ = inst.Start(now)

	if err := inst.Terminate(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.Status() != InstanceStatusTerminated {
		t.Fatalf("expected terminated, got %s", inst.Status())
	}

	if err := inst.Start(now); err == nil {
		t.Fatal("expected error starting terminated instance")
	}
}
