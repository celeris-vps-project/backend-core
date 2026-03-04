package domain

import (
	"testing"
	"time"
)

func makeTestNode(id, code string, totalSlots, usedSlots int, enabled bool) *HostNode {
	now := time.Now()
	return ReconstituteHostNode(
		id, code, "DE-fra", "region-1", code, "secret", "10.0.0.1", HostStatusOnline, "1.0",
		0, 0, 0, 0, &now, now,
		totalSlots, usedSlots, enabled,
	)
}

func TestResourcePool_TotalPhysicalSlots(t *testing.T) {
	nodes := []*HostNode{
		makeTestNode("n1", "DE-fra-01", 10, 3, true),
		makeTestNode("n2", "DE-fra-02", 20, 5, true),
		makeTestNode("n3", "DE-fra-03", 15, 15, true), // full
	}
	pool := NewResourcePool("r1", "DE-fra", "Frankfurt", nodes)

	if pool.TotalPhysicalSlots() != 45 {
		t.Fatalf("expected 45 total, got %d", pool.TotalPhysicalSlots())
	}
	if pool.UsedPhysicalSlots() != 23 {
		t.Fatalf("expected 23 used, got %d", pool.UsedPhysicalSlots())
	}
	if pool.AvailablePhysicalSlots() != 22 {
		t.Fatalf("expected 22 available, got %d", pool.AvailablePhysicalSlots())
	}
}

func TestResourcePool_SelectNode_LeastLoaded(t *testing.T) {
	nodes := []*HostNode{
		makeTestNode("n1", "DE-fra-01", 10, 8, true),  // 2 available
		makeTestNode("n2", "DE-fra-02", 20, 5, true),  // 15 available (best)
		makeTestNode("n3", "DE-fra-03", 15, 15, true), // 0 available
	}
	pool := NewResourcePool("r1", "DE-fra", "Frankfurt", nodes)

	selected, err := pool.SelectNode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected.ID() != "n2" {
		t.Fatalf("expected n2 (most available), got %s", selected.ID())
	}
}

func TestResourcePool_SelectNode_NoCapacity(t *testing.T) {
	nodes := []*HostNode{
		makeTestNode("n1", "DE-fra-01", 10, 10, true),
		makeTestNode("n2", "DE-fra-02", 5, 5, true),
	}
	pool := NewResourcePool("r1", "DE-fra", "Frankfurt", nodes)

	_, err := pool.SelectNode()
	if err == nil {
		t.Fatal("expected error when no capacity")
	}
}

func TestResourcePool_SelectNode_SkipsDisabled(t *testing.T) {
	nodes := []*HostNode{
		makeTestNode("n1", "DE-fra-01", 100, 0, false), // disabled — lots of slots but can't use
		makeTestNode("n2", "DE-fra-02", 10, 5, true),   // enabled — 5 available
	}
	pool := NewResourcePool("r1", "DE-fra", "Frankfurt", nodes)

	selected, err := pool.SelectNode()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected.ID() != "n2" {
		t.Fatalf("expected n2 (only enabled node), got %s", selected.ID())
	}
}

func TestResourcePool_HasCapacity(t *testing.T) {
	nodesWithCap := []*HostNode{makeTestNode("n1", "DE-fra-01", 10, 5, true)}
	noCap := []*HostNode{makeTestNode("n1", "DE-fra-01", 10, 10, true)}

	pool1 := NewResourcePool("r1", "DE-fra", "Frankfurt", nodesWithCap)
	pool2 := NewResourcePool("r1", "DE-fra", "Frankfurt", noCap)

	if !pool1.HasCapacity() {
		t.Fatal("expected pool1 to have capacity")
	}
	if pool2.HasCapacity() {
		t.Fatal("expected pool2 to have no capacity")
	}
}

func TestResourcePool_EmptyPool(t *testing.T) {
	pool := NewResourcePool("r1", "DE-fra", "Frankfurt", nil)

	if pool.TotalPhysicalSlots() != 0 {
		t.Fatalf("expected 0 total, got %d", pool.TotalPhysicalSlots())
	}
	if pool.HasCapacity() {
		t.Fatal("expected no capacity in empty pool")
	}
	_, err := pool.SelectNode()
	if err == nil {
		t.Fatal("expected error selecting from empty pool")
	}
}
