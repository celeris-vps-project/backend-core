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

type memProvisionRegionRepo struct {
	items map[string]*domain.Region
}

func newMemProvisionRegionRepo() *memProvisionRegionRepo {
	return &memProvisionRegionRepo{items: map[string]*domain.Region{}}
}

func (r *memProvisionRegionRepo) GetByID(id string) (*domain.Region, error) {
	region, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return region, nil
}

func (r *memProvisionRegionRepo) GetByCode(code string) (*domain.Region, error) {
	for _, region := range r.items {
		if region.Code() == code {
			return region, nil
		}
	}
	return nil, errors.New("not found")
}

func (r *memProvisionRegionRepo) ListAll() ([]*domain.Region, error) {
	out := make([]*domain.Region, 0, len(r.items))
	for _, region := range r.items {
		out = append(out, region)
	}
	return out, nil
}

func (r *memProvisionRegionRepo) ListActive() ([]*domain.Region, error) {
	var out []*domain.Region
	for _, region := range r.items {
		if region.IsActive() {
			out = append(out, region)
		}
	}
	return out, nil
}

func (r *memProvisionRegionRepo) Save(region *domain.Region) error {
	r.items[region.ID()] = region
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

type memProvisionNATPortRepo struct {
	items map[string]*domain.NATPortAllocation
}

func newMemProvisionNATPortRepo() *memProvisionNATPortRepo {
	return &memProvisionNATPortRepo{items: map[string]*domain.NATPortAllocation{}}
}

func (r *memProvisionNATPortRepo) ListByNodeID(nodeID string) ([]*domain.NATPortAllocation, error) {
	var out []*domain.NATPortAllocation
	for _, item := range r.items {
		if item.NodeID() == nodeID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *memProvisionNATPortRepo) ListByInstanceID(instanceID string) ([]*domain.NATPortAllocation, error) {
	var out []*domain.NATPortAllocation
	for _, item := range r.items {
		if item.InstanceID() == instanceID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *memProvisionNATPortRepo) SaveMany(allocations []*domain.NATPortAllocation) error {
	for _, allocation := range allocations {
		r.items[allocation.ID()] = allocation
	}
	return nil
}

func (r *memProvisionNATPortRepo) DeleteByInstanceID(instanceID string) error {
	for id, item := range r.items {
		if item.InstanceID() == instanceID {
			delete(r.items, id)
		}
	}
	return nil
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

type memProvisionStateCache struct {
	items map[string]*domain.NodeState
}

func newMemProvisionStateCache() *memProvisionStateCache {
	return &memProvisionStateCache{items: map[string]*domain.NodeState{}}
}

func (c *memProvisionStateCache) SetNodeState(nodeID string, state *domain.NodeState) error {
	c.items[nodeID] = state
	return nil
}

func (c *memProvisionStateCache) GetNodeState(nodeID string) (*domain.NodeState, error) {
	return c.items[nodeID], nil
}

func (c *memProvisionStateCache) GetAllNodeStates() (map[string]*domain.NodeState, error) {
	return c.items, nil
}

func (c *memProvisionStateCache) DeleteNodeState(nodeID string) error {
	delete(c.items, nodeID)
	return nil
}

func TestProvisioningAppService_CreateHostDefaultsNATPortRange(t *testing.T) {
	hostRepo := newMemProvisionHostRepo()
	regionRepo := newMemProvisionRegionRepo()
	idGen := &seqProvisionIDGen{}
	svc := NewProvisioningAppService(hostRepo, nil, nil, nil, regionRepo, nil, nil, nil, idGen, nil)

	node, err := svc.CreateHost("DE-fra-01", "DE-fra", "Frankfurt #1", "secret", 10, 0, 0, "", "")
	if err != nil {
		t.Fatalf("unexpected create host error: %v", err)
	}
	if node.NATPortStart() != DefaultNATPortStart || node.NATPortEnd() != DefaultNATPortEnd {
		t.Fatalf("expected default NAT range %d-%d, got %d-%d",
			DefaultNATPortStart, DefaultNATPortEnd, node.NATPortStart(), node.NATPortEnd())
	}
	if node.NATPortPoolSize() != DefaultNATPortEnd-DefaultNATPortStart+1 {
		t.Fatalf("unexpected NAT pool size: %d", node.NATPortPoolSize())
	}
	if node.NATBridge() != DefaultNATBridge {
		t.Fatalf("expected default NAT bridge %s, got %s", DefaultNATBridge, node.NATBridge())
	}

	stored, err := hostRepo.GetByID(node.ID())
	if err != nil {
		t.Fatalf("expected stored node: %v", err)
	}
	if stored.NATPortStart() != DefaultNATPortStart || stored.NATPortEnd() != DefaultNATPortEnd {
		t.Fatalf("stored node lost default NAT range: %d-%d", stored.NATPortStart(), stored.NATPortEnd())
	}
	if stored.NATBridge() != DefaultNATBridge {
		t.Fatalf("stored node lost default NAT bridge: %s", stored.NATBridge())
	}
}

func TestProvisioningAppService_ResolveHostIPPrefersNATEntryHost(t *testing.T) {
	hostRepo := newMemProvisionHostRepo()
	stateCache := newMemProvisionStateCache()
	idGen := &seqProvisionIDGen{}
	svc := NewProvisioningAppService(hostRepo, nil, nil, nil, newMemProvisionRegionRepo(), nil, nil, stateCache, idGen, nil)

	node, err := svc.CreateHost("DE-fra-01", "DE-fra", "Frankfurt #1", "secret", 10, 20000, 20010, "vmbr2", "nat.example.com")
	if err != nil {
		t.Fatalf("unexpected create host error: %v", err)
	}
	if err := stateCache.SetNodeState(node.ID(), &domain.NodeState{IP: "198.51.100.10"}); err != nil {
		t.Fatalf("unexpected state set error: %v", err)
	}

	if got := svc.resolveHostIP(node.ID()); got != "nat.example.com" {
		t.Fatalf("expected configured NAT entry host, got %q", got)
	}
}

func TestProvisioningAppService_ResolveHostIPFallsBackToAgentIP(t *testing.T) {
	hostRepo := newMemProvisionHostRepo()
	stateCache := newMemProvisionStateCache()
	idGen := &seqProvisionIDGen{}
	svc := NewProvisioningAppService(hostRepo, nil, nil, nil, newMemProvisionRegionRepo(), nil, nil, stateCache, idGen, nil)

	node, err := svc.CreateHost("DE-fra-01", "DE-fra", "Frankfurt #1", "secret", 10, 20000, 20010, "vmbr2", "")
	if err != nil {
		t.Fatalf("unexpected create host error: %v", err)
	}
	if err := stateCache.SetNodeState(node.ID(), &domain.NodeState{IP: "198.51.100.10"}); err != nil {
		t.Fatalf("unexpected state set error: %v", err)
	}

	if got := svc.resolveHostIP(node.ID()); got != "198.51.100.10" {
		t.Fatalf("expected agent IP fallback, got %q", got)
	}
}

func TestVPSProvisioner_ProvisionAssignsNATPortToTask(t *testing.T) {
	hostRepo := newMemProvisionHostRepo()
	poolRepo := newMemProvisionPoolRepo()
	taskRepo := newMemProvisionTaskRepo()
	ipRepo := newMemProvisionIPRepo()
	natPortRepo := newMemProvisionNATPortRepo()
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
		WithNATPortRepo(natPortRepo),
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
		NATPortCount:   3,
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
	if len(taskRepo.lastTask.Spec.NATForwards) != 3 {
		t.Fatalf("expected 3 NAT forwards, got %d", len(taskRepo.lastTask.Spec.NATForwards))
	}
	if taskRepo.lastTask.Spec.NATForwards[0].GuestPort != 22 || taskRepo.lastTask.Spec.NATForwards[2].GuestPort != 20002 {
		t.Fatalf("unexpected NAT forwards: %#v", taskRepo.lastTask.Spec.NATForwards)
	}
	if taskRepo.lastTask.Spec.IPv4 != "10.0.0.10" {
		t.Fatalf("expected NAT guest IPv4 10.0.0.10, got %s", taskRepo.lastTask.Spec.IPv4)
	}
	if taskRepo.lastTask.Spec.NetworkName != DefaultNATBridge {
		t.Fatalf("expected NAT network bridge %s, got %s", DefaultNATBridge, taskRepo.lastTask.Spec.NetworkName)
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
	natPorts, err := natPortRepo.ListByNodeID("node-1")
	if err != nil {
		t.Fatalf("unexpected NAT allocation list error: %v", err)
	}
	if len(natPorts) != 3 {
		t.Fatalf("expected 3 NAT port allocation rows, got %d", len(natPorts))
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
	if allocation.Address() != "10.0.0.10" {
		t.Fatalf("expected allocation guest ip 10.0.0.10, got %s", allocation.Address())
	}
}

func TestVPSProvisioner_AllocateNATPortIncrementsGuestIPv4(t *testing.T) {
	ipRepo := newMemProvisionIPRepo()
	natPortRepo := newMemProvisionNATPortRepo()
	idGen := &seqProvisionIDGen{}

	host, err := domain.NewHostNode("node-2", "DE-fra-02", "DE-fra", "Frankfurt #2", "secret")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if err := host.SetNATPortRange(20000, 20010); err != nil {
		t.Fatalf("unexpected NAT port range error: %v", err)
	}

	provisioner := NewVPSProvisioner(
		newMemProvisionHostRepo(),
		newMemProvisionPoolRepo(),
		newMemProvisionTaskRepo(),
		idGen,
		WithIPRepo(ipRepo),
		WithNATPortRepo(natPortRepo),
	)

	first, firstForwards, err := provisioner.allocateNATPorts(host, "inst-1", 1)
	if err != nil {
		t.Fatalf("unexpected first NAT allocation error: %v", err)
	}
	second, secondForwards, err := provisioner.allocateNATPorts(host, "inst-2", 1)
	if err != nil {
		t.Fatalf("unexpected second NAT allocation error: %v", err)
	}

	if first.Port() != 20000 || first.Address() != "10.0.0.10" {
		t.Fatalf("unexpected first allocation: port=%d address=%s", first.Port(), first.Address())
	}
	if second.Port() != 20001 || second.Address() != "10.0.0.11" {
		t.Fatalf("unexpected second allocation: port=%d address=%s", second.Port(), second.Address())
	}
	if len(firstForwards) != 1 || firstForwards[0].HostPort != 20000 {
		t.Fatalf("unexpected first forwards: %#v", firstForwards)
	}
	if len(secondForwards) != 1 || secondForwards[0].HostPort != 20001 {
		t.Fatalf("unexpected second forwards: %#v", secondForwards)
	}
}

func TestVPSProvisioner_AllocateNATPortsSkipsLegacyAllocatedPort(t *testing.T) {
	ipRepo := newMemProvisionIPRepo()
	natPortRepo := newMemProvisionNATPortRepo()
	idGen := &seqProvisionIDGen{}

	host, err := domain.NewHostNode("node-legacy", "DE-fra-legacy", "DE-fra", "Frankfurt legacy", "secret")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	if err := host.SetNATPortRange(20000, 20005); err != nil {
		t.Fatalf("unexpected NAT port range error: %v", err)
	}

	legacy, err := domain.NewNATPortAllocation("legacy-ip", host.ID(), "10.0.0.10", 20000)
	if err != nil {
		t.Fatalf("unexpected legacy allocation error: %v", err)
	}
	if err := legacy.Assign("old-inst"); err != nil {
		t.Fatalf("unexpected legacy assignment error: %v", err)
	}
	ipRepo.items[legacy.ID()] = legacy

	provisioner := NewVPSProvisioner(
		newMemProvisionHostRepo(),
		newMemProvisionPoolRepo(),
		newMemProvisionTaskRepo(),
		idGen,
		WithIPRepo(ipRepo),
		WithNATPortRepo(natPortRepo),
	)

	allocation, forwards, err := provisioner.allocateNATPorts(host, "new-inst", 2)
	if err != nil {
		t.Fatalf("unexpected NAT allocation error: %v", err)
	}

	if allocation.Port() != 20001 || allocation.Address() != "10.0.0.11" {
		t.Fatalf("expected allocation to skip legacy port, got port=%d address=%s", allocation.Port(), allocation.Address())
	}
	if len(forwards) != 2 || forwards[0].HostPort != 20001 || forwards[1].HostPort != 20002 {
		t.Fatalf("expected contiguous forwards [20001,20002], got %#v", forwards)
	}
}

func TestProvisioningAppService_ActiveNATForwardsIncludesLegacyAllocations(t *testing.T) {
	ipRepo := newMemProvisionIPRepo()
	natPortRepo := newMemProvisionNATPortRepo()

	legacy, err := domain.NewNATPortAllocation("legacy-ip", "node-legacy", "10.0.0.10", 20000)
	if err != nil {
		t.Fatalf("unexpected legacy allocation error: %v", err)
	}
	if err := legacy.Assign("old-inst"); err != nil {
		t.Fatalf("unexpected legacy assignment error: %v", err)
	}
	ipRepo.items[legacy.ID()] = legacy

	allocation, err := domain.NewNATPortForwardAllocation(
		"nat-1",
		"group-1",
		"node-legacy",
		"new-inst",
		"10.0.0.11",
		domain.NATProtocolTCP,
		20001,
		domain.DefaultSSHPort,
	)
	if err != nil {
		t.Fatalf("unexpected NAT port allocation error: %v", err)
	}
	if err := natPortRepo.SaveMany([]*domain.NATPortAllocation{allocation}); err != nil {
		t.Fatalf("unexpected NAT port save error: %v", err)
	}

	service := &ProvisioningAppService{ipRepo: ipRepo, natPortRepo: natPortRepo}
	forwards := service.activeNATForwards("node-legacy")
	if len(forwards) != 2 {
		t.Fatalf("expected new and legacy forwards, got %#v", forwards)
	}
	if forwards[0].HostPort != 20001 || forwards[1].HostPort != 20000 {
		t.Fatalf("unexpected forwards: %#v", forwards)
	}
}

func TestVPSProvisioner_ProvisionAssignsDedicatedIPv4ToTask(t *testing.T) {
	hostRepo := newMemProvisionHostRepo()
	poolRepo := newMemProvisionPoolRepo()
	taskRepo := newMemProvisionTaskRepo()
	ipRepo := newMemProvisionIPRepo()
	idGen := &seqProvisionIDGen{}

	host, err := domain.NewHostNode("node-3", "US-lax-01", "US-lax", "Los Angeles #1", "secret")
	if err != nil {
		t.Fatalf("unexpected host error: %v", err)
	}
	host.SetTotalSlots(4)
	host.SetRegionID("region-us")
	host.SetResourcePoolID("pool-3")
	hostRepo.items[host.ID()] = host

	pool, err := domain.NewResourcePool("pool-3", "lax-pool", "region-us")
	if err != nil {
		t.Fatalf("unexpected pool error: %v", err)
	}
	poolRepo.items[pool.ID()] = pool

	ip, err := domain.NewIPAddress("ip-ded-1", host.ID(), "203.0.113.10", 4)
	if err != nil {
		t.Fatalf("unexpected ip error: %v", err)
	}
	ipRepo.items[ip.ID()] = ip

	provisioner := NewVPSProvisioner(
		hostRepo,
		poolRepo,
		taskRepo,
		idGen,
		WithIPRepo(ipRepo),
	)

	_, err = provisioner.Provision(ProvisionCommand{
		InstanceID:     "inst-ded-1",
		ProductID:      "prod-1",
		ResourcePoolID: "pool-3",
		CustomerID:     "cust-1",
		OrderID:        "ord-1",
		Hostname:       "web-01",
		OS:             "ubuntu-24.04",
		CPU:            2,
		MemoryMB:       2048,
		DiskGB:         40,
		NetworkMode:    "dedicated",
	})
	if err != nil {
		t.Fatalf("unexpected provision error: %v", err)
	}
	if taskRepo.lastTask == nil {
		t.Fatal("expected task to be saved")
	}
	if taskRepo.lastTask.Spec.IPv4 != "203.0.113.10" {
		t.Fatalf("expected dedicated IPv4 in task, got %s", taskRepo.lastTask.Spec.IPv4)
	}
	if taskRepo.lastTask.Spec.NetworkMode != contracts.NetworkModeDedicated {
		t.Fatalf("expected dedicated network mode, got %s", taskRepo.lastTask.Spec.NetworkMode)
	}
	storedIP, err := ipRepo.GetByID("ip-ded-1")
	if err != nil {
		t.Fatalf("unexpected ip lookup error: %v", err)
	}
	if storedIP.InstanceID() != "inst-ded-1" {
		t.Fatalf("expected dedicated ip assigned to inst-ded-1, got %s", storedIP.InstanceID())
	}
}

func TestVPSProvisioner_ReleaseFreesSlotAndNATAllocation(t *testing.T) {
	hostRepo := newMemProvisionHostRepo()
	poolRepo := newMemProvisionPoolRepo()
	taskRepo := newMemProvisionTaskRepo()
	ipRepo := newMemProvisionIPRepo()
	natPortRepo := newMemProvisionNATPortRepo()
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

	alloc, err := domain.NewNATPortAllocation("ip-1", host.ID(), "10.0.0.15", 20005)
	if err != nil {
		t.Fatalf("unexpected NAT allocation error: %v", err)
	}
	if err := alloc.Assign("inst-rel-1"); err != nil {
		t.Fatalf("unexpected assign error: %v", err)
	}
	ipRepo.items[alloc.ID()] = alloc
	forward, err := domain.NewNATPortForwardAllocation("nat-1", "block-1", host.ID(), "inst-rel-1", "10.0.0.15", domain.NATProtocolTCP, 20005, domain.DefaultSSHPort)
	if err != nil {
		t.Fatalf("unexpected NAT port allocation error: %v", err)
	}
	natPortRepo.items[forward.ID()] = forward

	provisioner := NewVPSProvisioner(
		hostRepo,
		poolRepo,
		taskRepo,
		idGen,
		WithIPRepo(ipRepo),
		WithNATPortRepo(natPortRepo),
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
	remaining, err := natPortRepo.ListByInstanceID("inst-rel-1")
	if err != nil {
		t.Fatalf("unexpected NAT port allocation lookup error: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected NAT port allocation rows to be deleted, got %d", len(remaining))
	}
}
