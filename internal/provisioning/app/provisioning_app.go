package app

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"errors"
	"log"
	"time"
)

type IDGenerator interface{ NewID() string }

// ProvisioningAppService manages all provisioning-layer concerns:
// host nodes, IP pools, resource pools, regions, agent registration,
// bootstrap tokens, tasks, and capacity queries.
type ProvisioningAppService struct {
	hostRepo   domain.HostNodeRepository
	ipRepo     domain.IPAddressRepository
	taskRepo   domain.TaskRepository
	regionRepo domain.RegionRepository
	poolRepo   domain.ResourcePoolRepository
	btRepo     domain.BootstrapTokenRepository
	stateCache domain.NodeStateCache
	ids        IDGenerator
	bus        *eventbus.EventBus
}

func NewProvisioningAppService(
	hostRepo domain.HostNodeRepository,
	ipRepo domain.IPAddressRepository,
	taskRepo domain.TaskRepository,
	regionRepo domain.RegionRepository,
	poolRepo domain.ResourcePoolRepository,
	btRepo domain.BootstrapTokenRepository,
	stateCache domain.NodeStateCache,
	ids IDGenerator,
	bus *eventbus.EventBus,
) *ProvisioningAppService {
	return &ProvisioningAppService{
		hostRepo: hostRepo, ipRepo: ipRepo, taskRepo: taskRepo,
		regionRepo: regionRepo, poolRepo: poolRepo, btRepo: btRepo,
		stateCache: stateCache,
		ids:        ids, bus: bus,
	}
}

// StateCache returns the cache for external consumers (e.g. HTTP handlers).
func (s *ProvisioningAppService) StateCache() domain.NodeStateCache {
	return s.stateCache
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Host CRUD
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) CreateHost(code, location, name, secret string, totalSlots int) (*domain.HostNode, error) {
	id := s.ids.NewID()
	h, err := domain.NewHostNode(id, code, location, name, secret)
	if err != nil {
		return nil, err
	}
	if totalSlots > 0 {
		h.SetTotalSlots(totalSlots)
	}
	if regionID := s.ensureRegion(location); regionID != "" {
		h.SetRegionID(regionID)
	}
	if err := s.hostRepo.Save(h); err != nil {
		return nil, err
	}
	return h, nil
}

func (s *ProvisioningAppService) GetHost(id string) (*domain.HostNode, error) {
	return s.hostRepo.GetByID(id)
}
func (s *ProvisioningAppService) ListHosts() ([]*domain.HostNode, error) {
	return s.hostRepo.ListAll()
}
func (s *ProvisioningAppService) ListHostsByLocation(loc string) ([]*domain.HostNode, error) {
	return s.hostRepo.ListByLocation(loc)
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Host capacity management
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) EnableHost(id string) error {
	h, err := s.hostRepo.GetByID(id)
	if err != nil {
		return err
	}
	h.Enable()
	return s.hostRepo.Save(h)
}

func (s *ProvisioningAppService) DisableHost(id string) error {
	h, err := s.hostRepo.GetByID(id)
	if err != nil {
		return err
	}
	h.Disable()
	return s.hostRepo.Save(h)
}

func (s *ProvisioningAppService) AllocateSlot(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	if err := h.AllocateSlot(); err != nil {
		return err
	}
	return s.hostRepo.Save(h)
}

func (s *ProvisioningAppService) ReleaseSlot(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	if err := h.ReleaseSlot(); err != nil {
		return err
	}
	return s.hostRepo.Save(h)
}

func (s *ProvisioningAppService) AvailableLocations() ([]LocationSummary, error) {
	nodes, err := s.hostRepo.ListAll()
	if err != nil {
		return nil, err
	}
	locMap := make(map[string]*LocationSummary)
	for _, n := range nodes {
		loc := n.Location()
		summary, ok := locMap[loc]
		if !ok {
			summary = &LocationSummary{Location: loc}
			locMap[loc] = summary
		}
		summary.TotalNodes++
		summary.TotalSlots += n.TotalSlots()
		summary.AvailableSlots += n.AvailableSlots()
		if n.HasCapacity() {
			summary.AvailableNodes++
		}
	}
	result := make([]LocationSummary, 0, len(locMap))
	for _, v := range locMap {
		result = append(result, *v)
	}
	return result, nil
}

type LocationSummary struct {
	Location       string `json:"location"`
	TotalNodes     int    `json:"total_nodes"`
	AvailableNodes int    `json:"available_nodes"`
	TotalSlots     int    `json:"total_slots"`
	AvailableSlots int    `json:"available_slots"`
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Agent registration & heartbeat (writes to CACHE, not database)
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) RegisterAgent(reg contracts.AgentRegistration) (*contracts.RegistrationResult, error) {
	bt, err := s.btRepo.GetByToken(reg.BootstrapToken)
	if err != nil {
		return nil, errors.New("app_error: invalid bootstrap token")
	}
	if !bt.IsValid() {
		return nil, errors.New("app_error: bootstrap token expired or already used")
	}

	nodeID := bt.NodeID()
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return nil, errors.New("app_error: node bound to bootstrap token not found")
	}

	nodeToken, err := domain.GenerateNodeToken()
	if err != nil {
		return nil, errors.New("app_error: failed to generate node token")
	}

	h.SetNodeToken(nodeToken)
	if err := s.hostRepo.Save(h); err != nil {
		return nil, err
	}

	if err := bt.Consume(); err != nil {
		return nil, err
	}
	if err := s.btRepo.Save(bt); err != nil {
		return nil, err
	}

	now := time.Now()
	state := &domain.NodeState{
		Status:     domain.HostStatusOnline,
		IP:         reg.IP,
		AgentVer:   reg.Version,
		LastSeenAt: now,
	}
	if err := s.stateCache.SetNodeState(nodeID, state); err != nil {
		return nil, err
	}

	if s.bus != nil {
		s.bus.Publish(events.NodeStateUpdatedEvent{
			NodeID:   nodeID,
			Status:   state.Status,
			IP:       state.IP,
			AgentVer: state.AgentVer,
			LastSeen: now.Format(time.RFC3339),
		})
	}

	log.Printf("[provisioning] agent registered for node %s via bootstrap token %s", nodeID, bt.ID())
	return &contracts.RegistrationResult{NodeID: nodeID, NodeToken: nodeToken}, nil
}

func (s *ProvisioningAppService) ensureRegion(location string) string {
	if location == "" {
		location = "unknown"
	}
	existing, err := s.regionRepo.GetByCode(location)
	if err == nil {
		return existing.ID()
	}
	id := s.ids.NewID()
	region, err := domain.NewRegion(id, location, location, "")
	if err != nil {
		log.Printf("[provisioning] failed to create region for location %q: %v", location, err)
		return ""
	}
	if err := s.regionRepo.Save(region); err != nil {
		log.Printf("[provisioning] failed to save auto-created region %q: %v", location, err)
		return ""
	}
	log.Printf("[provisioning] auto-created region %q (id=%s) for agent location", location, id)
	return id
}

func (s *ProvisioningAppService) Heartbeat(hb contracts.Heartbeat) (*contracts.HeartbeatAck, error) {
	_, err := s.hostRepo.GetByID(hb.NodeID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	existing, _ := s.stateCache.GetNodeState(hb.NodeID)
	state := &domain.NodeState{
		Status:     domain.HostStatusOnline,
		CPUUsage:   hb.CPUUsage,
		MemUsage:   hb.MemUsage,
		DiskUsage:  hb.DiskUsage,
		VMCount:    hb.VMCount,
		LastSeenAt: now,
	}
	if existing != nil {
		state.IP = existing.IP
		state.AgentVer = existing.AgentVer
	}
	if err := s.stateCache.SetNodeState(hb.NodeID, state); err != nil {
		return nil, err
	}

	if s.bus != nil {
		s.bus.Publish(events.NodeStateUpdatedEvent{
			NodeID:    hb.NodeID,
			Status:    state.Status,
			IP:        state.IP,
			AgentVer:  state.AgentVer,
			CPUUsage:  state.CPUUsage,
			MemUsage:  state.MemUsage,
			DiskUsage: state.DiskUsage,
			VMCount:   state.VMCount,
			LastSeen:  now.Format(time.RFC3339),
		})
	}

	tasks, err := s.taskRepo.ListPendingByNodeID(hb.NodeID)
	if err != nil {
		return &contracts.HeartbeatAck{OK: true}, nil
	}
	return &contracts.HeartbeatAck{OK: true, Tasks: tasks}, nil
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Task management
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) ReportTaskResult(result contracts.TaskResult) error {
	task, err := s.taskRepo.GetByID(result.TaskID)
	if err != nil {
		return err
	}
	task.Status = result.Status
	task.Error = result.Error
	task.FinishedAt = result.FinishedAt
	return s.taskRepo.Save(task)
}

func (s *ProvisioningAppService) EnqueueTask(nodeID string, taskType contracts.TaskType, spec contracts.ProvisionSpec) (*contracts.Task, error) {
	task := &contracts.Task{
		ID:        s.ids.NewID(),
		NodeID:    nodeID,
		Type:      taskType,
		Status:    contracts.TaskStatusQueued,
		Spec:      spec,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if err := s.taskRepo.Save(task); err != nil {
		return nil, err
	}
	return task, nil
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// IP management
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) AddIP(nodeID, address string, version int) (*domain.IPAddress, error) {
	id := s.ids.NewID()
	ip, err := domain.NewIPAddress(id, nodeID, address, version)
	if err != nil {
		return nil, err
	}
	if err := s.ipRepo.Save(ip); err != nil {
		return nil, err
	}
	return ip, nil
}

func (s *ProvisioningAppService) ListIPs(nodeID string) ([]*domain.IPAddress, error) {
	return s.ipRepo.ListByNodeID(nodeID)
}

func (s *ProvisioningAppService) AllocateIP(nodeID string, version int, instanceID string) (*domain.IPAddress, error) {
	ip, err := s.ipRepo.FindAvailable(nodeID, version)
	if err != nil {
		return nil, err
	}
	if err := ip.Assign(instanceID); err != nil {
		return nil, err
	}
	if err := s.ipRepo.Save(ip); err != nil {
		return nil, err
	}
	return ip, nil
}

func (s *ProvisioningAppService) ReleaseIP(ipID string) error {
	ip, err := s.ipRepo.GetByID(ipID)
	if err != nil {
		return err
	}
	ip.Release()
	return s.ipRepo.Save(ip)
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// NAT port management
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

// SetNATPortRange configures the NAT port range for a host node.
func (s *ProvisioningAppService) SetNATPortRange(nodeID string, start, end int) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	if err := h.SetNATPortRange(start, end); err != nil {
		return err
	}
	return s.hostRepo.Save(h)
}

// ClearNATPortRange removes the NAT port range configuration for a node.
func (s *ProvisioningAppService) ClearNATPortRange(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	h.ClearNATPortRange()
	return s.hostRepo.Save(h)
}

// AllocateNATPort dynamically allocates a NAT port from the node's configured range.
// It finds a free port, creates an IPAddress record (mode=nat), assigns it to the instance,
// and returns the allocated port along with the host's public IP from NodeStateCache.
func (s *ProvisioningAppService) AllocateNATPort(nodeID, instanceID string) (hostIP string, port int, err error) {
	node, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return "", 0, err
	}
	if !node.HasNATPortPool() {
		return "", 0, errors.New("app_error: node has no NAT port pool configured")
	}

	// 1. Try to reuse a previously released NAT port
	existing, findErr := s.ipRepo.FindAvailableNAT(nodeID)
	if findErr == nil && existing != nil {
		if err := existing.Assign(instanceID); err != nil {
			return "", 0, err
		}
		if err := s.ipRepo.Save(existing); err != nil {
			return "", 0, err
		}
		hostIP = s.resolveHostIP(nodeID)
		return hostIP, existing.Port(), nil
	}

	// 2. Find a free port from the node's range
	usedPorts, err := s.ipRepo.ListNATPortsByNodeID(nodeID)
	if err != nil {
		return "", 0, err
	}
	usedSet := make(map[int]struct{}, len(usedPorts))
	for _, p := range usedPorts {
		usedSet[p] = struct{}{}
	}
	freePort, err := node.FindFreeNATPort(usedSet)
	if err != nil {
		return "", 0, err
	}

	// 3. Create and assign a new NAT port allocation
	alloc, err := domain.NewNATPortAllocation(s.ids.NewID(), nodeID, freePort)
	if err != nil {
		return "", 0, err
	}
	if err := alloc.Assign(instanceID); err != nil {
		return "", 0, err
	}
	if err := s.ipRepo.Save(alloc); err != nil {
		return "", 0, err
	}

	hostIP = s.resolveHostIP(nodeID)
	return hostIP, freePort, nil
}

// ReleaseNATPort releases a NAT port allocation back to the pool.
func (s *ProvisioningAppService) ReleaseNATPort(ipID string) error {
	return s.ReleaseIP(ipID) // same lifecycle as dedicated IP
}

// ListNATPorts returns all NAT port allocations on a node.
func (s *ProvisioningAppService) ListNATPorts(nodeID string) ([]int, error) {
	return s.ipRepo.ListNATPortsByNodeID(nodeID)
}

// resolveHostIP gets the host's public IP from the NodeStateCache.
func (s *ProvisioningAppService) resolveHostIP(nodeID string) string {
	state, err := s.stateCache.GetNodeState(nodeID)
	if err != nil || state == nil {
		return ""
	}
	return state.IP
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Region CRUD
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) CreateRegion(code, name, flagIcon string) (*domain.Region, error) {
	id := s.ids.NewID()
	r, err := domain.NewRegion(id, code, name, flagIcon)
	if err != nil {
		return nil, err
	}
	if err := s.regionRepo.Save(r); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *ProvisioningAppService) GetRegion(id string) (*domain.Region, error) {
	return s.regionRepo.GetByID(id)
}

func (s *ProvisioningAppService) ListRegions() ([]*domain.Region, error) {
	return s.regionRepo.ListAll()
}

func (s *ProvisioningAppService) ListActiveRegions() ([]*domain.Region, error) {
	return s.regionRepo.ListActive()
}

func (s *ProvisioningAppService) ActivateRegion(id string) error {
	r, err := s.regionRepo.GetByID(id)
	if err != nil {
		return err
	}
	r.Activate()
	return s.regionRepo.Save(r)
}

func (s *ProvisioningAppService) DeactivateRegion(id string) error {
	r, err := s.regionRepo.GetByID(id)
	if err != nil {
		return err
	}
	r.Deactivate()
	return s.regionRepo.Save(r)
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Resource Pool CRUD (pure physical pool --- no sales attributes)
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) CreateResourcePool(name, regionID string) (*domain.ResourcePool, error) {
	id := s.ids.NewID()
	pool, err := domain.NewResourcePool(id, name, regionID)
	if err != nil {
		return nil, err
	}
	if err := s.poolRepo.Save(pool); err != nil {
		return nil, err
	}
	return pool, nil
}

func (s *ProvisioningAppService) GetResourcePool(poolID string) (*domain.ResourcePool, error) {
	pool, err := s.poolRepo.GetByID(poolID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.hostRepo.ListEnabledByResourcePoolID(poolID)
	if err != nil {
		return pool, nil
	}
	pool.WithNodes(nodes)
	return pool, nil
}

func (s *ProvisioningAppService) ListResourcePools() ([]*domain.ResourcePool, error) {
	return s.poolRepo.ListAll()
}

func (s *ProvisioningAppService) ListActiveResourcePools() ([]*domain.ResourcePool, error) {
	return s.poolRepo.ListActive()
}

func (s *ProvisioningAppService) UpdateResourcePool(poolID, name, regionID string) (*domain.ResourcePool, error) {
	pool, err := s.poolRepo.GetByID(poolID)
	if err != nil {
		return nil, err
	}
	if name != "" {
		pool.SetName(name)
	}
	if regionID != "" {
		pool.SetRegionID(regionID)
	}
	if err := s.poolRepo.Save(pool); err != nil {
		return nil, err
	}
	return pool, nil
}

func (s *ProvisioningAppService) ActivateResourcePool(id string) error {
	pool, err := s.poolRepo.GetByID(id)
	if err != nil {
		return err
	}
	pool.Activate()
	return s.poolRepo.Save(pool)
}

func (s *ProvisioningAppService) DeactivateResourcePool(id string) error {
	pool, err := s.poolRepo.GetByID(id)
	if err != nil {
		return err
	}
	pool.Deactivate()
	return s.poolRepo.Save(pool)
}

func (s *ProvisioningAppService) AssignNodeToPool(nodeID, poolID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	if _, err := s.poolRepo.GetByID(poolID); err != nil {
		return err
	}
	h.SetResourcePoolID(poolID)
	return s.hostRepo.Save(h)
}

func (s *ProvisioningAppService) RemoveNodeFromPool(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	h.SetResourcePoolID("")
	return s.hostRepo.Save(h)
}

func (s *ProvisioningAppService) SaveResourcePool(pool *domain.ResourcePool) error {
	return s.poolRepo.Save(pool)
}

// PoolCapacitySummary is a read-model for the admin panel showing physical
// capacity of a resource pool.
type PoolCapacitySummary struct {
	PoolID         string `json:"pool_id"`
	PoolName       string `json:"pool_name"`
	RegionID       string `json:"region_id"`
	Status         string `json:"status"`
	TotalNodes     int    `json:"total_nodes"`
	EnabledNodes   int    `json:"enabled_nodes"`
	TotalSlots     int    `json:"total_slots"`
	UsedSlots      int    `json:"used_slots"`
	AvailableSlots int    `json:"available_slots"`
}

func (s *ProvisioningAppService) ListPoolCapacities() ([]PoolCapacitySummary, error) {
	pools, err := s.poolRepo.ListAll()
	if err != nil {
		return nil, err
	}
	summaries := make([]PoolCapacitySummary, 0, len(pools))
	for _, p := range pools {
		allNodes, err := s.hostRepo.ListByResourcePoolID(p.ID())
		if err != nil {
			continue
		}
		enabledNodes, err := s.hostRepo.ListEnabledByResourcePoolID(p.ID())
		if err != nil {
			continue
		}
		p.WithNodes(enabledNodes)
		summaries = append(summaries, PoolCapacitySummary{
			PoolID:         p.ID(),
			PoolName:       p.Name(),
			RegionID:       p.RegionID(),
			Status:         p.Status(),
			TotalNodes:     len(allNodes),
			EnabledNodes:   len(enabledNodes),
			TotalSlots:     p.TotalPhysicalSlots(),
			UsedSlots:      p.UsedPhysicalSlots(),
			AvailableSlots: p.AvailablePhysicalSlots(),
		})
	}
	return summaries, nil
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Node Token Authentication (used by gRPC interceptor)
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) ValidateNodeToken(token string) (string, error) {
	h, err := s.hostRepo.GetByNodeToken(token)
	if err != nil {
		return "", errors.New("app_error: invalid node token")
	}
	return h.ID(), nil
}

func (s *ProvisioningAppService) RevokeNodeToken(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	h.RevokeNodeToken()
	return s.hostRepo.Save(h)
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Bootstrap Token Management (admin API)
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) CreateBootstrapToken(nodeID string, ttl time.Duration, description string) (*domain.BootstrapToken, error) {
	if _, err := s.hostRepo.GetByID(nodeID); err != nil {
		return nil, errors.New("app_error: target node not found")
	}
	id := s.ids.NewID()
	bt, err := domain.NewBootstrapToken(id, nodeID, ttl, description)
	if err != nil {
		return nil, err
	}
	if err := s.btRepo.Save(bt); err != nil {
		return nil, err
	}
	return bt, nil
}

func (s *ProvisioningAppService) ListBootstrapTokens() ([]*domain.BootstrapToken, error) {
	return s.btRepo.ListAll()
}

func (s *ProvisioningAppService) RevokeBootstrapToken(id string) error {
	return s.btRepo.Delete(id)
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// HostNodeRepository accessor (for anti-corruption adapters)
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

func (s *ProvisioningAppService) HostRepo() domain.HostNodeRepository {
	return s.hostRepo
}
