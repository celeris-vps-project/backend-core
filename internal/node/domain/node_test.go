package domain

import (
	"testing"
	"time"
)

func TestHostNode_RegisterAndHeartbeat(t *testing.T) {
	n, err := NewHostNode("n-1", "DE-fra-01", "DE-fra", "Frankfurt #1", "s3cret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.Status() != HostStatusOffline {
		t.Fatalf("expected offline, got %s", n.Status())
	}

	now := time.Now()
	n.Register("10.0.0.1", "v0.1.0", now)
	if n.Status() != HostStatusOnline {
		t.Fatalf("expected online, got %s", n.Status())
	}
	if n.IP() != "10.0.0.1" {
		t.Fatalf("expected ip 10.0.0.1, got %s", n.IP())
	}

	n.RecordHeartbeat(45.2, 60.0, 30.5, 3, now)
	if n.VMCount() != 3 {
		t.Fatalf("expected 3 vms, got %d", n.VMCount())
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
