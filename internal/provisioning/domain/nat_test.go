package domain

import (
	"testing"
	"time"
)

// ──── IPAddress NAT tests ────────────────────────────────────────────

func TestNewNATPortAllocation_Valid(t *testing.T) {
	alloc, err := NewNATPortAllocation("ip-1", "node-1", 20001)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if alloc.Mode() != NetworkModeNAT {
		t.Fatalf("expected mode=nat, got %s", alloc.Mode())
	}
	if alloc.Port() != 20001 {
		t.Fatalf("expected port=20001, got %d", alloc.Port())
	}
	if alloc.Address() != "" {
		t.Fatalf("expected empty address for NAT, got %s", alloc.Address())
	}
	if !alloc.IsNAT() {
		t.Fatal("expected IsNAT=true")
	}
	if alloc.IsDedicated() {
		t.Fatal("expected IsDedicated=false")
	}
	if !alloc.IsAvailable() {
		t.Fatal("expected IsAvailable=true")
	}
}

func TestNewNATPortAllocation_InvalidPort(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too high", 70000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewNATPortAllocation("ip-1", "node-1", tc.port)
			if err == nil {
				t.Fatal("expected error for invalid port")
			}
		})
	}
}

func TestNATPortAllocation_AssignAndRelease(t *testing.T) {
	alloc, _ := NewNATPortAllocation("ip-1", "node-1", 20001)

	if err := alloc.Assign("inst-1"); err != nil {
		t.Fatalf("unexpected error assigning: %v", err)
	}
	if alloc.IsAvailable() {
		t.Fatal("should not be available after assign")
	}
	if alloc.InstanceID() != "inst-1" {
		t.Fatalf("expected instanceID=inst-1, got %s", alloc.InstanceID())
	}

	// Double-assign should fail
	if err := alloc.Assign("inst-2"); err == nil {
		t.Fatal("expected error on double-assign")
	}

	// Release
	alloc.Release()
	if !alloc.IsAvailable() {
		t.Fatal("should be available after release")
	}
}

func TestReconstituteIPAddressFull_NAT(t *testing.T) {
	ip := ReconstituteIPAddressFull("ip-1", "node-1", "", 4, NetworkModeNAT, 30000, "inst-1")
	if ip.Mode() != NetworkModeNAT {
		t.Fatalf("expected mode=nat, got %s", ip.Mode())
	}
	if ip.Port() != 30000 {
		t.Fatalf("expected port=30000, got %d", ip.Port())
	}
	if ip.InstanceID() != "inst-1" {
		t.Fatalf("expected instanceID=inst-1, got %s", ip.InstanceID())
	}
}

func TestReconstituteIPAddressFull_DefaultMode(t *testing.T) {
	ip := ReconstituteIPAddressFull("ip-1", "node-1", "1.2.3.4", 4, "", 0, "")
	if ip.Mode() != NetworkModeDedicated {
		t.Fatalf("expected mode=dedicated for empty, got %s", ip.Mode())
	}
}

func TestDedicatedIPBackwardCompat(t *testing.T) {
	ip := ReconstituteIPAddress("ip-1", "node-1", "1.2.3.4", 4, "")
	if ip.Mode() != NetworkModeDedicated {
		t.Fatalf("expected mode=dedicated, got %s", ip.Mode())
	}
	if ip.Port() != 0 {
		t.Fatalf("expected port=0, got %d", ip.Port())
	}
	if !ip.IsDedicated() {
		t.Fatal("expected IsDedicated=true")
	}
	if ip.IsNAT() {
		t.Fatal("expected IsNAT=false")
	}
}

// ──── HostNode NAT port pool tests ───────────────────────────────────

func TestHostNode_SetNATPortRange(t *testing.T) {
	node := ReconstituteHostNode("n1", "DE-01", "DE", "", "", "node1", "secret", "", time.Now(), 10, 0, true)

	if err := node.SetNATPortRange(20000, 60000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.NATPortStart() != 20000 {
		t.Fatalf("expected start=20000, got %d", node.NATPortStart())
	}
	if node.NATPortEnd() != 60000 {
		t.Fatalf("expected end=60000, got %d", node.NATPortEnd())
	}
	if !node.HasNATPortPool() {
		t.Fatal("expected HasNATPortPool=true")
	}
	if node.NATPortPoolSize() != 40001 {
		t.Fatalf("expected pool size=40001, got %d", node.NATPortPoolSize())
	}
}

func TestHostNode_SetNATPortRange_Validation(t *testing.T) {
	node := ReconstituteHostNode("n1", "DE-01", "DE", "", "", "node1", "secret", "", time.Now(), 10, 0, true)

	tests := []struct {
		name       string
		start, end int
	}{
		{"start below 1024", 100, 60000},
		{"end below start", 30000, 20000},
		{"end equals start", 20000, 20000},
		{"end exceeds 65535", 20000, 70000},
		{"zero start", 0, 60000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := node.SetNATPortRange(tc.start, tc.end); err == nil {
				t.Fatal("expected error for invalid range")
			}
		})
	}
}

func TestHostNode_FindFreeNATPort(t *testing.T) {
	node := ReconstituteHostNodeFull("n1", "DE-01", "DE", "", "", "node1", "secret", "", time.Now(), 10, 0, true, 20000, 20005)

	// No ports used
	port, err := node.FindFreeNATPort(map[int]struct{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 20000 {
		t.Fatalf("expected first free port=20000, got %d", port)
	}

	// Some ports used
	used := map[int]struct{}{20000: {}, 20001: {}, 20002: {}}
	port, err = node.FindFreeNATPort(used)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 20003 {
		t.Fatalf("expected port=20003, got %d", port)
	}

	// All ports used
	allUsed := map[int]struct{}{20000: {}, 20001: {}, 20002: {}, 20003: {}, 20004: {}, 20005: {}}
	_, err = node.FindFreeNATPort(allUsed)
	if err == nil {
		t.Fatal("expected error when all ports are used")
	}
}

func TestHostNode_FindFreeNATPort_NoPoolConfigured(t *testing.T) {
	node := ReconstituteHostNode("n1", "DE-01", "DE", "", "", "node1", "secret", "", time.Now(), 10, 0, true)

	_, err := node.FindFreeNATPort(map[int]struct{}{})
	if err == nil {
		t.Fatal("expected error when no NAT pool configured")
	}
}

func TestHostNode_ClearNATPortRange(t *testing.T) {
	node := ReconstituteHostNodeFull("n1", "DE-01", "DE", "", "", "node1", "secret", "", time.Now(), 10, 0, true, 20000, 60000)

	if !node.HasNATPortPool() {
		t.Fatal("expected HasNATPortPool=true before clear")
	}

	node.ClearNATPortRange()

	if node.HasNATPortPool() {
		t.Fatal("expected HasNATPortPool=false after clear")
	}
	if node.NATPortPoolSize() != 0 {
		t.Fatalf("expected pool size=0 after clear, got %d", node.NATPortPoolSize())
	}
}

func TestHostNode_HasNATPortPool_NotConfigured(t *testing.T) {
	node := ReconstituteHostNode("n1", "DE-01", "DE", "", "", "node1", "secret", "", time.Now(), 10, 0, true)
	if node.HasNATPortPool() {
		t.Fatal("expected HasNATPortPool=false for unconfigured node")
	}
}
