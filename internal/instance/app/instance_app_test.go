package app

import (
	"backend-core/internal/instance/domain"
	"errors"
	"testing"
)

// ---- In-memory test doubles ----

type memNodeRepo struct{ items map[string]*domain.Node }

func newMemNodeRepo() *memNodeRepo { return &memNodeRepo{items: map[string]*domain.Node{}} }

func (r *memNodeRepo) GetByID(id string) (*domain.Node, error) {
	n, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return n, nil
}
func (r *memNodeRepo) ListAll() ([]*domain.Node, error) {
	out := make([]*domain.Node, 0, len(r.items))
	for _, n := range r.items {
		out = append(out, n)
	}
	return out, nil
}
func (r *memNodeRepo) ListByLocation(loc string) ([]*domain.Node, error) {
	var out []*domain.Node
	for _, n := range r.items {
		if n.Location() == loc {
			out = append(out, n)
		}
	}
	return out, nil
}
func (r *memNodeRepo) Save(n *domain.Node) error { r.items[n.ID()] = n; return nil }

type memInstRepo struct{ items map[string]*domain.Instance }

func newMemInstRepo() *memInstRepo { return &memInstRepo{items: map[string]*domain.Instance{}} }

func (r *memInstRepo) GetByID(id string) (*domain.Instance, error) {
	i, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return i, nil
}
func (r *memInstRepo) ListByCustomerID(cid string) ([]*domain.Instance, error) {
	var out []*domain.Instance
	for _, i := range r.items {
		if i.CustomerID() == cid {
			out = append(out, i)
		}
	}
	return out, nil
}
func (r *memInstRepo) ListByNodeID(nid string) ([]*domain.Instance, error) {
	var out []*domain.Instance
	for _, i := range r.items {
		if i.NodeID() == nid {
			out = append(out, i)
		}
	}
	return out, nil
}
func (r *memInstRepo) Save(i *domain.Instance) error { r.items[i.ID()] = i; return nil }

type seqIDGen struct{ counter int }

func (g *seqIDGen) NewID() string {
	g.counter++
	return "id-" + string(rune('0'+g.counter))
}

// ---- Tests ----

func TestPurchaseInstance_AllocatesSlot(t *testing.T) {
	nodeRepo := newMemNodeRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen)

	// Create a node with 2 slots
	node, err := svc.CreateNode("DE-fra-01", "DE-fra", "Frankfurt #1", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Purchase first instance
	inst1, err := svc.PurchaseInstance("cust-1", "ord-1", node.ID(), "web-01", "vps-starter", "ubuntu-22.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst1.Status() != domain.InstanceStatusPending {
		t.Fatalf("expected pending, got %s", inst1.Status())
	}

	// Purchase second instance
	_, err = svc.PurchaseInstance("cust-2", "ord-2", node.ID(), "web-02", "vps-starter", "ubuntu-22.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Third should fail — no capacity
	_, err = svc.PurchaseInstance("cust-3", "ord-3", node.ID(), "web-03", "vps-starter", "ubuntu-22.04", 2, 2048, 40)
	if err == nil {
		t.Fatal("expected error for no capacity")
	}

	// Verify node used slots
	storedNode, _ := nodeRepo.GetByID(node.ID())
	if storedNode.UsedSlots() != 2 {
		t.Fatalf("expected 2 used slots, got %d", storedNode.UsedSlots())
	}
}

func TestTerminateInstance_ReleasesSlot(t *testing.T) {
	nodeRepo := newMemNodeRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen)

	node, _ := svc.CreateNode("US-slc-01", "US-slc", "Salt Lake City #1", 1)
	inst, _ := svc.PurchaseInstance("cust-1", "ord-1", node.ID(), "app-01", "vps-pro", "debian-12", 4, 8192, 100)

	_ = svc.StartInstance(inst.ID())
	if err := svc.TerminateInstance(inst.ID()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	storedNode, _ := nodeRepo.GetByID(node.ID())
	if storedNode.UsedSlots() != 0 {
		t.Fatalf("expected 0 used slots after terminate, got %d", storedNode.UsedSlots())
	}
}

func TestAvailableLocations(t *testing.T) {
	nodeRepo := newMemNodeRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen)

	_, _ = svc.CreateNode("DE-fra-01", "DE-fra", "Frankfurt #1", 5)
	_, _ = svc.CreateNode("DE-fra-02", "DE-fra", "Frankfurt #2", 3)
	_, _ = svc.CreateNode("US-slc-01", "US-slc", "Salt Lake City #1", 10)

	locs, err := svc.AvailableLocations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(locs) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locs))
	}
}
