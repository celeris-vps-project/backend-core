package app

import (
	"backend-core/internal/instance/domain"
	nodeDomain "backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
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
func (r *memInstRepo) GetByOrderID(orderID string) (*domain.Instance, error) {
	for _, i := range r.items {
		if i.OrderID() == orderID {
			return i, nil
		}
	}
	return nil, errors.New("not found")
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

type scheduledTask struct {
	nodeID   string
	taskType contracts.TaskType
	spec     contracts.ProvisionSpec
}

type memLifecycleScheduler struct {
	tasks []scheduledTask
}

func (s *memLifecycleScheduler) Enqueue(nodeID string, taskType contracts.TaskType, spec contracts.ProvisionSpec) error {
	s.tasks = append(s.tasks, scheduledTask{
		nodeID:   nodeID,
		taskType: taskType,
		spec:     spec,
	})
	return nil
}

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

func TestCreatePendingInstance_DoesNotAllocateNodeOrSlot(t *testing.T) {
	nodeRepo := newMemNodeAllocatorRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen, nil)

	node := createTestHostNode(nodeRepo, "node-3", "JP-tyo-01", "JP-tyo", "Tokyo #1", 2)

	inst, err := svc.CreatePendingInstance("cust-1", "ord-1", "JP-tyo", "web-01", "vps-basic", "ubuntu-24.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst.NodeID() != "" {
		t.Fatalf("expected empty node assignment, got %s", inst.NodeID())
	}

	storedNode, _ := nodeRepo.GetByID(node.ID())
	if storedNode.UsedSlots() != 0 {
		t.Fatalf("expected 0 used slots after pending instance creation, got %d", storedNode.UsedSlots())
	}
}

func TestConfirmProvisioning_AssignsNodeAndNetworkDetails(t *testing.T) {
	nodeRepo := newMemNodeAllocatorRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen, nil)

	inst, err := svc.CreatePendingInstance("cust-1", "ord-1", "US-lax", "app-01", "vps-pro", "debian-12", 4, 8192, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := svc.ConfirmProvisioning(inst.ID(), "node-real-1", "198.51.100.10", "", "nat", 22001); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, err := svc.GetInstance(inst.ID())
	if err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if stored.NodeID() != "node-real-1" {
		t.Fatalf("expected node-real-1, got %s", stored.NodeID())
	}
	if stored.IPv4() != "198.51.100.10" {
		t.Fatalf("expected ipv4 assigned, got %s", stored.IPv4())
	}
	if stored.NetworkMode() != "nat" {
		t.Fatalf("expected nat mode, got %s", stored.NetworkMode())
	}
	if stored.NATPort() != 22001 {
		t.Fatalf("expected NAT port 22001, got %d", stored.NATPort())
	}
	if stored.Status() != domain.InstanceStatusRunning {
		t.Fatalf("expected running status, got %s", stored.Status())
	}
}

func TestStartInstance_WithLifecycleSchedulerEnqueuesTask(t *testing.T) {
	nodeRepo := newMemNodeAllocatorRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	scheduler := &memLifecycleScheduler{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen, nil)
	svc.SetLifecycleScheduler(scheduler)

	_ = createTestHostNode(nodeRepo, "node-4", "US-lax-01", "US-lax", "Los Angeles #1", 2)
	inst, err := svc.PurchaseInstance("cust-1", "ord-1", "US-lax", "web-01", "vps-basic", "ubuntu-24.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected purchase error: %v", err)
	}

	if err := svc.StartInstance(inst.ID()); err != nil {
		t.Fatalf("unexpected start error: %v", err)
	}
	if len(scheduler.tasks) != 1 {
		t.Fatalf("expected 1 scheduled task, got %d", len(scheduler.tasks))
	}
	if scheduler.tasks[0].taskType != contracts.TaskStart {
		t.Fatalf("expected start task, got %s", scheduler.tasks[0].taskType)
	}
	stored, _ := svc.GetInstance(inst.ID())
	if stored.Status() != domain.InstanceStatusPending {
		t.Fatalf("expected status to remain pending until task completion, got %s", stored.Status())
	}
}

func TestStopInstance_WithLifecycleSchedulerEnqueuesTask(t *testing.T) {
	nodeRepo := newMemNodeAllocatorRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	scheduler := &memLifecycleScheduler{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen, nil)

	_ = createTestHostNode(nodeRepo, "node-5", "US-sea-01", "US-sea", "Seattle #1", 2)
	inst, err := svc.PurchaseInstance("cust-1", "ord-1", "US-sea", "web-01", "vps-basic", "ubuntu-24.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected purchase error: %v", err)
	}
	if err := svc.StartInstance(inst.ID()); err != nil {
		t.Fatalf("unexpected local start error: %v", err)
	}
	svc.SetLifecycleScheduler(scheduler)

	if err := svc.StopInstance(inst.ID()); err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
	if len(scheduler.tasks) != 1 {
		t.Fatalf("expected 1 scheduled task, got %d", len(scheduler.tasks))
	}
	if scheduler.tasks[0].taskType != contracts.TaskStop {
		t.Fatalf("expected stop task, got %s", scheduler.tasks[0].taskType)
	}
	stored, _ := svc.GetInstance(inst.ID())
	if stored.Status() != domain.InstanceStatusRunning {
		t.Fatalf("expected status to remain running until task completion, got %s", stored.Status())
	}
}

func TestTerminateInstance_WithLifecycleSchedulerEnqueuesDeprovision(t *testing.T) {
	nodeRepo := newMemNodeAllocatorRepo()
	instRepo := newMemInstRepo()
	idGen := &seqIDGen{}
	scheduler := &memLifecycleScheduler{}
	svc := NewInstanceAppService(nodeRepo, instRepo, idGen, nil)

	_ = createTestHostNode(nodeRepo, "node-6", "SG-sin-01", "SG-sin", "Singapore #1", 2)
	inst, err := svc.PurchaseInstance("cust-1", "ord-1", "SG-sin", "web-01", "vps-basic", "ubuntu-24.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected purchase error: %v", err)
	}
	if err := svc.StartInstance(inst.ID()); err != nil {
		t.Fatalf("unexpected local start error: %v", err)
	}
	if err := svc.ConfirmProvisioning(inst.ID(), inst.NodeID(), "198.51.100.20", "", "nat", 22010); err != nil {
		t.Fatalf("unexpected provisioning confirm error: %v", err)
	}
	svc.SetLifecycleScheduler(scheduler)

	if err := svc.TerminateInstance(inst.ID()); err != nil {
		t.Fatalf("unexpected terminate error: %v", err)
	}
	if len(scheduler.tasks) != 1 {
		t.Fatalf("expected 1 scheduled task, got %d", len(scheduler.tasks))
	}
	if scheduler.tasks[0].taskType != contracts.TaskDeprovision {
		t.Fatalf("expected deprovision task, got %s", scheduler.tasks[0].taskType)
	}
	if scheduler.tasks[0].spec.NetworkMode != contracts.NetworkModeNAT || scheduler.tasks[0].spec.NATPort != 22010 {
		t.Fatalf("expected NAT details to be propagated, got mode=%s port=%d", scheduler.tasks[0].spec.NetworkMode, scheduler.tasks[0].spec.NATPort)
	}
	stored, _ := svc.GetInstance(inst.ID())
	if stored.Status() != domain.InstanceStatusRunning {
		t.Fatalf("expected status to remain running until deprovision completes, got %s", stored.Status())
	}
}
