package domain

import (
	"testing"
)

func TestHostNode_Capacity(t *testing.T) {
	n, err := NewHostNode("n-3", "DE-fra-01", "DE-fra", "Frankfurt #1", "s3cret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n.SetTotalSlots(3)

	if n.AvailableSlots() != 3 {
		t.Fatalf("expected 3 available, got %d", n.AvailableSlots())
	}

	for i := 0; i < 3; i++ {
		if err := n.AllocateSlot(); err != nil {
			t.Fatalf("unexpected error on alloc %d: %v", i, err)
		}
	}
	if n.AvailableSlots() != 0 {
		t.Fatalf("expected 0 available, got %d", n.AvailableSlots())
	}
	if n.HasCapacity() {
		t.Fatal("expected no capacity")
	}

	if err := n.AllocateSlot(); err == nil {
		t.Fatal("expected error allocating on full node")
	}

	if err := n.ReleaseSlot(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.AvailableSlots() != 1 {
		t.Fatalf("expected 1 available, got %d", n.AvailableSlots())
	}
}

func TestHostNode_Disabled(t *testing.T) {
	n, _ := NewHostNode("n-4", "US-slc-01", "US-slc", "Salt Lake City #1", "s3cret")
	n.SetTotalSlots(10)
	n.Disable()

	if err := n.AllocateSlot(); err == nil {
		t.Fatal("expected error allocating on disabled node")
	}
	if n.HasCapacity() {
		t.Fatal("disabled node should report no capacity")
	}
}

func TestHostNode_EnabledByDefault(t *testing.T) {
	n, _ := NewHostNode("n-5", "JP-tky-01", "JP-tky", "Tokyo #1", "key")
	if !n.Enabled() {
		t.Fatal("new host node should be enabled by default")
	}
}

func TestHostNode_ValidateSecret(t *testing.T) {
	n, _ := NewHostNode("n-2", "US-slc-01", "US-slc", "Salt Lake City #1", "mykey")
	if !n.ValidateSecret("mykey") {
		t.Fatal("expected valid secret")
	}
	if n.ValidateSecret("wrong") {
		t.Fatal("expected invalid secret")
	}
}

func TestIPAddress_AssignRelease(t *testing.T) {
	ip, err := NewIPAddress("ip-1", "n-1", "185.1.2.3", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ip.IsAvailable() {
		t.Fatal("expected available")
	}
	if err := ip.Assign("inst-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip.IsAvailable() {
		t.Fatal("expected assigned")
	}
	if err := ip.Assign("inst-2"); err == nil {
		t.Fatal("expected error double-assigning")
	}
	ip.Release()
	if !ip.IsAvailable() {
		t.Fatal("expected available after release")
	}
}
