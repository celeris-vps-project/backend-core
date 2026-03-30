package app

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"errors"
	"fmt"
	"testing"
)

type seqProvisionIDGen struct {
	next int
}

func (g *seqProvisionIDGen) NewID() string {
	g.next++
	return fmt.Sprintf("id-%d", g.next)
}

type memProvisionHostRepo struct {
	items map[string]*domain.HostNode
}

func newMemProvisionHostRepo() *memProvisionHostRepo {
	return &memProvisionHostRepo{items: map[string]*domain.HostNode{}}
}

func (r *memProvisionHostRepo) GetByID(id string) (*domain.HostNode, error) {
	node, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return node, nil
}

func (r *memProvisionHostRepo) GetByCode(code string) (*domain.HostNode, error) {
	for _, node := range r.items {
		if node.Code() == code {
			return node, nil
		}
	}
	return nil, errors.New("not found")
}

func (r *memProvisionHostRepo) GetByNodeToken(token string) (*domain.HostNode, error) {
	for _, node := range r.items {
		if node.NodeToken() == token {
			return node, nil
		}
	}
	return nil, errors.New("not found")
}

func (r *memProvisionHostRepo) ListAll() ([]*domain.HostNode, error) {
	out := make([]*domain.HostNode, 0, len(r.items))
	for _, node := range r.items {
		out = append(out, node)
	}
	return out, nil
}

func (r *memProvisionHostRepo) ListByLocation(location string) ([]*domain.HostNode, error) {
	var out []*domain.HostNode
	for _, node := range r.items {
		if node.Location() == location {
			out = append(out, node)
		}
	}
	return out, nil
}

func (r *memProvisionHostRepo) ListByRegionID(regionID string) ([]*domain.HostNode, error) {
	var out []*domain.HostNode
	for _, node := range r.items {
		if node.RegionID() == regionID {
			out = append(out, node)
		}
	}
	return out, nil
}

func (r *memProvisionHostRepo) ListEnabledByRegionID(regionID string) ([]*domain.HostNode, error) {
	var out []*domain.HostNode
	for _, node := range r.items {
		if node.RegionID() == regionID && node.Enabled() {
			out = append(out, node)
		}
	}
	return out, nil
}

func (r *memProvisionHostRepo) ListByResourcePoolID(poolID string) ([]*domain.HostNode, error) {
	var out []*domain.HostNode
	for _, node := range r.items {
		if node.ResourcePoolID() == poolID {
			out = append(out, node)
		}
	}
	return out, nil
}

func (r *memProvisionHostRepo) ListEnabledByResourcePoolID(poolID string) ([]*domain.HostNode, error) {
	var out []*domain.HostNode
	for _, node := range r.items {
		if node.ResourcePoolID() == poolID && node.Enabled() {
			out = append(out, node)
		}
	}
	return out, nil
}

func (r *memProvisionHostRepo) Save(node *domain.HostNode) error {
	r.items[node.ID()] = node
	return nil
}

func (r *memProvisionHostRepo) AllocateSlotAtomic(nodeID string) error {
	node, err := r.GetByID(nodeID)
	if err != nil {
		return err
	}
	if err := node.AllocateSlot(); err != nil {
		return err
	}
	return r.Save(node)
}

func (r *memProvisionHostRepo) ReleaseSlotAtomic(nodeID string) error {
	node, err := r.GetByID(nodeID)
	if err != nil {
		return err
	}
	if err := node.ReleaseSlot(); err != nil {
		return err
	}
	return r.Save(node)
}

type memProvisionPoolRepo struct {
	items map[string]*domain.ResourcePool
}

func newMemProvisionPoolRepo() *memProvisionPoolRepo {
	return &memProvisionPoolRepo{items: map[string]*domain.ResourcePool{}}
}

func (r *memProvisionPoolRepo) GetByID(id string) (*domain.ResourcePool, error) {
	pool, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return pool, nil
}

func (r *memProvisionPoolRepo) GetByRegionID(regionID string) ([]*domain.ResourcePool, error) {
	var out []*domain.ResourcePool
	for _, pool := range r.items {
		if pool.RegionID() == regionID {
			out = append(out, pool)
		}
	}
	return out, nil
}

func (r *memProvisionPoolRepo) ListAll() ([]*domain.ResourcePool, error) {
	out := make([]*domain.ResourcePool, 0, len(r.items))
	for _, pool := range r.items {
		out = append(out, pool)
	}
	return out, nil
}

func (r *memProvisionPoolRepo) ListActive() ([]*domain.ResourcePool, error) {
	var out []*domain.ResourcePool
	for _, pool := range r.items {
		if pool.IsActive() {
			out = append(out, pool)
		}
	}
	return out, nil
}

func (r *memProvisionPoolRepo) Save(pool *domain.ResourcePool) error {
	r.items[pool.ID()] = pool
	return nil
}

func (r *memProvisionPoolRepo) Delete(id string) error {
	delete(r.items, id)
	return nil
}

type memProvisionIPRepo struct {
	items map[string]*domain.IPAddress
}

func newMemProvisionIPRepo() *memProvisionIPRepo {
	return &memProvisionIPRepo{items: map[string]*domain.IPAddress{}}
}

func (r *memProvisionIPRepo) GetByID(id string) (*domain.IPAddress, error) {
	ip, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return ip, nil
}

func (r *memProvisionIPRepo) ListByNodeID(nodeID string) ([]*domain.IPAddress, error) {
	var out []*domain.IPAddress
	for _, ip := range r.items {
		if ip.NodeID() == nodeID {
			out = append(out, ip)
		}
	}
	return out, nil
}

func (r *memProvisionIPRepo) FindByInstanceID(instanceID string) (*domain.IPAddress, error) {
	for _, ip := range r.items {
		if ip.InstanceID() == instanceID {
			return ip, nil
		}
	}
	return nil, errors.New("not found")
}

func (r *memProvisionIPRepo) FindAvailable(nodeID string, version int) (*domain.IPAddress, error) {
	for _, ip := range r.items {
		if ip.NodeID() == nodeID && ip.Version() == version && ip.IsAvailable() {
			return ip, nil
		}
	}
	return nil, errors.New("not found")
}

func (r *memProvisionIPRepo) Save(ip *domain.IPAddress) error {
	r.items[ip.ID()] = ip
	return nil
}

func (r *memProvisionIPRepo) ListNATPortsByNodeID(nodeID string) ([]int, error) {
	var ports []int
	for _, ip := range r.items {
		if ip.NodeID() == nodeID && ip.IsNAT() {
			ports = append(ports, ip.Port())
		}
	}
	return ports, nil
}

func (r *memProvisionIPRepo) FindAvailableNAT(nodeID string) (*domain.IPAddress, error) {
	for _, ip := range r.items {
		if ip.NodeID() == nodeID && ip.IsNAT() && ip.IsAvailable() {
			return ip, nil
		}
	}
	return nil, errors.New("not found")
}

type memProvisionTaskRepo struct {
	items    map[string]*contracts.Task
	lastTask *contracts.Task
}

func newMemProvisionTaskRepo() *memProvisionTaskRepo {
	return &memProvisionTaskRepo{items: map[string]*contracts.Task{}}
}

func (r *memProvisionTaskRepo) GetByID(id string) (*contracts.Task, error) {
	task, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return task, nil
}

func (r *memProvisionTaskRepo) ListPendingByNodeID(nodeID string) ([]contracts.Task, error) {
	var out []contracts.Task
	for _, task := range r.items {
		if task.NodeID == nodeID && task.Status == contracts.TaskStatusQueued {
			out = append(out, *task)
		}
	}
	return out, nil
}

func (r *memProvisionTaskRepo) Save(task *contracts.Task) error {
	copyTask := *task
	r.items[task.ID] = &copyTask
	r.lastTask = &copyTask
	return nil
}

func TestVPSProvisioner_ProvisionAssignsNATPortToTask(t *testing.T) {
	hostRepo := newMemProvisionHostRepo()
	poolRepo := newMemProvisionPoolRepo()
	taskRepo := newMemProvisionTaskRepo()
	ipRepo := newMemProvisionIPRepo()
	idGen := &seqProvisionIDGen{}

	host, err := domain.NewHostNode("node-1", "DE-fra-01", "DE-fra", "Frankfurt #1", "secret")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	host.SetTotalSlots(4)
	host.SetRegionID("region-de")
	host.SetResourcePoolID("pool-1")
	if err := host.SetNATPortRange(20000, 20010); err != nil {
		t.Fatalf("unexpected NAT port range error: %v", err)
	}
	hostRepo.items[host.ID()] = host

	pool, err := domain.NewResourcePool("pool-1", "fra-pool", "region-de")
	if err != nil {
		t.Fatalf("unexpected pool error: %v", err)
	}
	poolRepo.items[pool.ID()] = pool

	provisioner := NewVPSProvisioner(
		hostRepo,
		poolRepo,
		taskRepo,
		idGen,
		WithIPRepo(ipRepo),
	)

	result, err := provisioner.Provision(ProvisionCommand{
		InstanceID:     "inst-1",
		ProductID:      "prod-1",
		ResourcePoolID: "pool-1",
		CustomerID:     "cust-1",
		OrderID:        "ord-1",
		Hostname:       "web-01",
		OS:             "ubuntu-24.04",
		CPU:            2,
		MemoryMB:       2048,
		DiskGB:         40,
		NetworkMode:    "nat",
	})
	if err != nil {
		t.Fatalf("unexpected provision error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected successful provisioning result")
	}
	if result.InstanceID != "inst-1" {
		t.Fatalf("expected instance inst-1, got %s", result.InstanceID)
	}
	if result.NodeID != "node-1" {
		t.Fatalf("expected node node-1, got %s", result.NodeID)
	}

	if taskRepo.lastTask == nil {
		t.Fatal("expected task to be saved")
	}
	if taskRepo.lastTask.Spec.InstanceID != "inst-1" {
		t.Fatalf("expected task instance inst-1, got %s", taskRepo.lastTask.Spec.InstanceID)
	}
	if taskRepo.lastTask.Spec.NetworkMode != contracts.NetworkModeNAT {
		t.Fatalf("expected task network mode nat, got %s", taskRepo.lastTask.Spec.NetworkMode)
	}
	if taskRepo.lastTask.Spec.NATPort != 20000 {
		t.Fatalf("expected NAT port 20000, got %d", taskRepo.lastTask.Spec.NATPort)
	}

	storedHost, err := hostRepo.GetByID("node-1")
	if err != nil {
		t.Fatalf("unexpected host lookup error: %v", err)
	}
	if storedHost.UsedSlots() != 1 {
		t.Fatalf("expected used slots 1, got %d", storedHost.UsedSlots())
	}

	ports, err := ipRepo.ListNATPortsByNodeID("node-1")
	if err != nil {
		t.Fatalf("unexpected NAT list error: %v", err)
	}
	if len(ports) != 1 || ports[0] != 20000 {
		t.Fatalf("expected allocated NAT port [20000], got %v", ports)
	}

	var allocation *domain.IPAddress
	for _, ip := range ipRepo.items {
		allocation = ip
		break
	}
	if allocation == nil {
		t.Fatal("expected NAT allocation record to be stored")
	}
	if !allocation.IsNAT() {
		t.Fatal("expected NAT allocation mode")
	}
	if allocation.InstanceID() != "inst-1" {
		t.Fatalf("expected allocation instance inst-1, got %s", allocation.InstanceID())
	}
}

func TestVPSProvisioner_ReleaseFreesSlotAndNATAllocation(t *testing.T) {
	hostRepo := newMemProvisionHostRepo()
	poolRepo := newMemProvisionPoolRepo()
	taskRepo := newMemProvisionTaskRepo()
	ipRepo := newMemProvisionIPRepo()
	idGen := &seqProvisionIDGen{}

	host, err := domain.NewHostNode("node-2", "DE-fra-02", "DE-fra", "Frankfurt #2", "secret")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	host.SetTotalSlots(2)
	host.SetRegionID("region-de")
	host.SetResourcePoolID("pool-2")
	if err := host.AllocateSlot(); err != nil {
		t.Fatalf("unexpected slot allocate error: %v", err)
	}
	hostRepo.items[host.ID()] = host

	alloc, err := domain.NewNATPortAllocation("ip-1", host.ID(), 20005)
	if err != nil {
		t.Fatalf("unexpected NAT allocation error: %v", err)
	}
	if err := alloc.Assign("inst-rel-1"); err != nil {
		t.Fatalf("unexpected assign error: %v", err)
	}
	ipRepo.items[alloc.ID()] = alloc

	provisioner := NewVPSProvisioner(
		hostRepo,
		poolRepo,
		taskRepo,
		idGen,
		WithIPRepo(ipRepo),
	)

	if err := provisioner.Release(ProvisionCommand{
		InstanceID: "inst-rel-1",
		NodeID:     host.ID(),
	}); err != nil {
		t.Fatalf("unexpected release error: %v", err)
	}

	storedHost, err := hostRepo.GetByID(host.ID())
	if err != nil {
		t.Fatalf("unexpected host lookup error: %v", err)
	}
	if storedHost.UsedSlots() != 0 {
		t.Fatalf("expected used slots 0 after release, got %d", storedHost.UsedSlots())
	}

	storedAlloc, err := ipRepo.GetByID("ip-1")
	if err != nil {
		t.Fatalf("unexpected ip lookup error: %v", err)
	}
	if !storedAlloc.IsAvailable() {
		t.Fatalf("expected NAT allocation to be released, got instance=%s", storedAlloc.InstanceID())
	}
}
