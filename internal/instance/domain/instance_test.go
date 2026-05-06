package domain

import (
	"testing"
	"time"
)

func TestInstanceLifecycle(t *testing.T) {
	inst, err := NewInstance("ins-1", "cust-1", "ord-1", "node-1", "web-01", "vps-starter", "ubuntu-22.04", "", 2, 2048, 40, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.ControlStatus() != InstanceControlStatusProvisioning {
		t.Fatalf("expected provisioning, got %s", inst.ControlStatus())
	}

	now := time.Now()
	if err := inst.MarkProvisioned(now); err != nil {
		t.Fatalf("unexpected provisioned error: %v", err)
	}
	if inst.ControlStatus() != InstanceControlStatusActive {
		t.Fatalf("expected active, got %s", inst.ControlStatus())
	}

	if err := inst.Start(now); err != nil {
		t.Fatalf("unexpected start error: %v", err)
	}
	if inst.ControlStatus() != InstanceControlStatusActive {
		t.Fatalf("expected control status to remain active, got %s", inst.ControlStatus())
	}

	if err := inst.Stop(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.ControlStatus() != InstanceControlStatusActive {
		t.Fatalf("expected control status to remain active after stop, got %s", inst.ControlStatus())
	}

	if err := inst.Start(now); err != nil {
		t.Fatalf("unexpected error restarting: %v", err)
	}
	if inst.StartedAt() == nil {
		t.Fatal("expected started timestamp")
	}
}

func TestInstanceSuspendUnsuspend(t *testing.T) {
	inst, _ := NewInstance("ins-2", "cust-1", "ord-1", "node-1", "db-01", "vps-pro", "debian-12", "", 4, 8192, 100, 2000)
	now := time.Now()
	_ = inst.MarkProvisioned(now)

	if err := inst.Suspend(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.ControlStatus() != InstanceControlStatusSuspended {
		t.Fatalf("expected suspended, got %s", inst.ControlStatus())
	}

	if err := inst.Unsuspend(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.ControlStatus() != InstanceControlStatusActive {
		t.Fatalf("expected active, got %s", inst.ControlStatus())
	}
}

func TestInstanceTerminate(t *testing.T) {
	inst, _ := NewInstance("ins-3", "cust-1", "ord-1", "node-1", "app-01", "vps-starter", "centos-9", "", 1, 1024, 20, 500)
	now := time.Now()
	_ = inst.MarkProvisioned(now)

	if err := inst.Terminate(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.ControlStatus() != InstanceControlStatusTerminated {
		t.Fatalf("expected terminated, got %s", inst.ControlStatus())
	}

	if err := inst.Start(now); err == nil {
		t.Fatal("expected error starting terminated instance")
	}
}
