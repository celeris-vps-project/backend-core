package app

import (
	"backend-core/internal/instance/domain"
	nodeDomain "backend-core/internal/node/domain"
	"errors"
	"testing"
)

// ---- In-memory test doubles ----

// memNodeAllocatorRepo implements domain.NodeAllocatorRepository using HostNode.
type memNodeAllocatorRepo struct {
	items map[string]*nodeDomain.HostNode
}

func newMemNodeAllocatorRepo() *memNodeAllocatorRepo {
	return &memNodeAllocatorRepo{items: map[string]*nodeDomain.HostNode{}}
}

func (r *memNodeAllocatorRepo) GetByID(id string) (domain.NodeAllocator, error) {
	n, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return n, nil
}
func (r *memNodeAllocatorRepo) ListAll() ([]domain.NodeAllocator, error) {
	out := make([]domain.NodeAllocator, 0, len(r.items))
	for _, n := range r.items {
		out = append(out, n)
	}
	return out, nil
}
func (r *memNodeAllocatorRepo) ListByLocation(loc string) ([]domain.NodeAllocator, error) {
	var out []domain.NodeAllocator
	for _, n := range r.items {
		if n.Location() == loc {
			out = append(out, n)
		}
	}
	return out, nil
}
func (r *memNodeAllocatorRepo) Save(n domain.NodeAllocator) error {
	hn := n.(*nodeDomain.HostNode)
	r.items[hn.ID()] = hn
	return nil
}

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

// createTestHostNode is a helper to create a HostNode with capacity and save it.
func createTestHostNode(repo *memNodeAllocatorRepo, id, code, location, name string, totalSlots int) *nodeDomain.HostNode {
	h, _ := nodeDomain.NewHostNode(id, code, location, name, "test-secret")
	h.SetTotalSlots(totalSlots)
	repo.items[id] = h
	return h
}

// ---- Tests ----

func TestPurchaseInstance_AllocatesSlot(t *testing.T) {
	nodeRepo := newMemNodeAllocatorRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen, nil)

	// Create a host node with 2 slots
	node := createTestHostNode(nodeRepo, "node-1", "DE-fra-01", "DE-fra", "Frankfurt #1", 2)

	// Purchase first instance (by region, not node ID)
	inst1, err := svc.PurchaseInstance("cust-1", "ord-1", "DE-fra", "web-01", "vps-starter", "ubuntu-22.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst1.Status() != domain.InstanceStatusPending {
		t.Fatalf("expected pending, got %s", inst1.Status())
	}

	// Purchase second instance
	_, err = svc.PurchaseInstance("cust-2", "ord-2", "DE-fra", "web-02", "vps-starter", "ubuntu-22.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Third should fail — no capacity
	_, err = svc.PurchaseInstance("cust-3", "ord-3", "DE-fra", "web-03", "vps-starter", "ubuntu-22.04", 2, 2048, 40)
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
	nodeRepo := newMemNodeAllocatorRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen, nil)

	_ = createTestHostNode(nodeRepo, "node-2", "US-slc-01", "US-slc", "Salt Lake City #1", 1)
	inst, _ := svc.PurchaseInstance("cust-1", "ord-1", "US-slc", "app-01", "vps-pro", "debian-12", 4, 8192, 100)

	_ = svc.StartInstance(inst.ID())
	if err := svc.TerminateInstance(inst.ID()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	storedNode, _ := nodeRepo.GetByID("node-2")
	if storedNode.UsedSlots() != 0 {
		t.Fatalf("expected 0 used slots after terminate, got %d", storedNode.UsedSlots())
	}
}
