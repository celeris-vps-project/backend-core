package app

import (
	"backend-core/internal/node/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"errors"
	"log"
	"time"
)

type IDGenerator interface{ NewID() string }

type NodeAppService struct {
	hostRepo   domain.HostNodeRepository
	ipRepo     domain.IPAddressRepository
	taskRepo   domain.TaskRepository
	regionRepo domain.RegionRepository
	poolRepo   domain.ResourcePoolRepository
	btRepo     domain.BootstrapTokenRepository // bootstrap token storage
	stateCache domain.NodeStateCache
	ids        IDGenerator
	bus        *eventbus.EventBus
}

func NewNodeAppService(
	hostRepo domain.HostNodeRepository,
	ipRepo domain.IPAddressRepository,
	taskRepo domain.TaskRepository,
	regionRepo domain.RegionRepository,
	poolRepo domain.ResourcePoolRepository,
	btRepo domain.BootstrapTokenRepository,
	stateCache domain.NodeStateCache,
	ids IDGenerator,
	bus *eventbus.EventBus,
) *NodeAppService {
	return &NodeAppService{
		hostRepo: hostRepo, ipRepo: ipRepo, taskRepo: taskRepo,
		regionRepo: regionRepo, poolRepo: poolRepo, btRepo: btRepo,
		stateCache: stateCache,
		ids:        ids, bus: bus,
	}
}

// StateCache returns the cache for external consumers (e.g. HTTP handlers).
func (s *NodeAppService) StateCache() domain.NodeStateCache {
	return s.stateCache
}

// ──────────────────────────────────────────────────────────────────────
// Host CRUD
// ──────────────────────────────────────────────────────────────────────

func (s *NodeAppService) CreateHost(code, location, name, secret string, totalSlots int) (*domain.HostNode, error) {
	id := s.ids.NewID()
	h, err := domain.NewHostNode(id, code, location, name, secret)
	if err != nil {
		return nil, err
	}
	if totalSlots > 0 {
		h.SetTotalSlots(totalSlots)
	}

	// Auto-create region from location and link it to the node
	if regionID := s.ensureRegion(location); regionID != "" {
		h.SetRegionID(regionID)
	}

	if err := s.hostRepo.Save(h); err != nil {
		return nil, err
	}
	return h, nil
}

func (s *NodeAppService) GetHost(id string) (*domain.HostNode, error) { return s.hostRepo.GetByID(id) }
func (s *NodeAppService) ListHosts() ([]*domain.HostNode, error)      { return s.hostRepo.ListAll() }
func (s *NodeAppService) ListHostsByLocation(loc string) ([]*domain.HostNode, error) {
	return s.hostRepo.ListByLocation(loc)
}

// ──────────────────────────────────────────────────────────────────────
// Host capacity management
// ──────────────────────────────────────────────────────────────────────

func (s *NodeAppService) EnableHost(id string) error {
	h, err := s.hostRepo.GetByID(id)
	if err != nil {
		return err
	}
	h.Enable()
	return s.hostRepo.Save(h)
}

func (s *NodeAppService) DisableHost(id string) error {
	h, err := s.hostRepo.GetByID(id)
	if err != nil {
		return err
	}
	h.Disable()
	return s.hostRepo.Save(h)
}

// AllocateSlot reserves one instance slot on the given node.
func (s *NodeAppService) AllocateSlot(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	if err := h.AllocateSlot(); err != nil {
		return err
	}
	return s.hostRepo.Save(h)
}

// ReleaseSlot frees one instance slot on the given node.
func (s *NodeAppService) ReleaseSlot(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	if err := h.ReleaseSlot(); err != nil {
		return err
	}
	return s.hostRepo.Save(h)
}

// AvailableLocations returns a summary of capacity grouped by location.
func (s *NodeAppService) AvailableLocations() ([]LocationSummary, error) {
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

// ──────────────────────────────────────────────────────────────────────
// Agent registration & heartbeat (writes to CACHE, not database)
// ──────────────────────────────────────────────────────────────────────

func (s *NodeAppService) RegisterAgent(reg contracts.AgentRegistration) (*contracts.RegistrationResult, error) {
	// 1. Validate the bootstrap token
	bt, err := s.btRepo.GetByToken(reg.BootstrapToken)
	if err != nil {
		return nil, errors.New("app_error: invalid bootstrap token")
	}
	if !bt.IsValid() {
		return nil, errors.New("app_error: bootstrap token expired or already used")
	}

	// 2. Resolve the target node from the bootstrap token's binding.
	//    The node MUST already exist (created via the admin panel).
	nodeID := bt.NodeID()
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return nil, errors.New("app_error: node bound to bootstrap token not found — create it in the admin panel first")
	}

	// 3. Generate a permanent node token
	nodeToken, err := domain.GenerateNodeToken()
	if err != nil {
		return nil, errors.New("app_error: failed to generate node token")
	}

	// 4. Issue a new permanent node token; all other node config (location,
	//    total_slots, region, etc.) is managed via the admin panel and must
	//    NOT be overridden by the agent.
	h.SetNodeToken(nodeToken)
	if err := s.hostRepo.Save(h); err != nil {
		return nil, err
	}

	// 5. Consume the bootstrap token (uses its own bound nodeID)
	if err := bt.Consume(); err != nil {
		return nil, err
	}
	if err := s.btRepo.Save(bt); err != nil {
		return nil, err
	}

	// 6. Write runtime state to cache
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

	// 7. Publish real-time event
	if s.bus != nil {
		s.bus.Publish(events.NodeStateUpdatedEvent{
			NodeID:   nodeID,
			Status:   state.Status,
			IP:       state.IP,
			AgentVer: state.AgentVer,
			LastSeen: now.Format(time.RFC3339),
		})
	}

	log.Printf("[node] agent registered for node %s via bootstrap token %s", nodeID, bt.ID())
	return &contracts.RegistrationResult{NodeID: nodeID, NodeToken: nodeToken}, nil
}

// ensureRegion looks up a region by the location code. If none exists, it
// auto-creates an active region so that every agent node is always associated
// with a region entry.
func (s *NodeAppService) ensureRegion(location string) string {
	if location == "" {
		location = "unknown"
	}

	// Try to find an existing region whose code matches the location
	existing, err := s.regionRepo.GetByCode(location)
	if err == nil {
		return existing.ID()
	}

	// Not found — auto-create a new region
	id := s.ids.NewID()
	region, err := domain.NewRegion(id, location, location, "")
	if err != nil {
		log.Printf("[node] failed to create region for location %q: %v", location, err)
		return ""
	}
	if err := s.regionRepo.Save(region); err != nil {
		log.Printf("[node] failed to save auto-created region %q: %v", location, err)
		return ""
	}
	log.Printf("[node] auto-created region %q (id=%s) for agent location", location, id)
	return id
}

func (s *NodeAppService) Heartbeat(hb contracts.Heartbeat) (*contracts.HeartbeatAck, error) {
	// Verify the node exists in the database
	_, err := s.hostRepo.GetByID(hb.NodeID)
	if err != nil {
		return nil, err
	}

	// Update runtime state in cache only (no database write)
	now := time.Now()
	// Preserve existing state fields (IP, AgentVer) if available
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

	// Publish real-time event for WebSocket subscribers
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

	// Return any queued tasks for this node
	tasks, err := s.taskRepo.ListPendingByNodeID(hb.NodeID)
	if err != nil {
		return &contracts.HeartbeatAck{OK: true}, nil
	}
	return &contracts.HeartbeatAck{OK: true, Tasks: tasks}, nil
}

// ──────────────────────────────────────────────────────────────────────
// Task result callback
// ──────────────────────────────────────────────────────────────────────

func (s *NodeAppService) ReportTaskResult(result contracts.TaskResult) error {
	task, err := s.taskRepo.GetByID(result.TaskID)
	if err != nil {
		return err
	}
	task.Status = result.Status
	task.Error = result.Error
	task.FinishedAt = result.FinishedAt
	return s.taskRepo.Save(task)
}

// ──────────────────────────────────────────────────────────────────────
// Enqueue a task (called by instance domain or internally)
// ──────────────────────────────────────────────────────────────────────

func (s *NodeAppService) EnqueueTask(nodeID string, taskType contracts.TaskType, spec contracts.ProvisionSpec) (*contracts.Task, error) {
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

// ──────────────────────────────────────────────────────────────────────
// IP management
// ──────────────────────────────────────────────────────────────────────

func (s *NodeAppService) AddIP(nodeID, address string, version int) (*domain.IPAddress, error) {
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

func (s *NodeAppService) ListIPs(nodeID string) ([]*domain.IPAddress, error) {
	return s.ipRepo.ListByNodeID(nodeID)
}

func (s *NodeAppService) AllocateIP(nodeID string, version int, instanceID string) (*domain.IPAddress, error) {
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

func (s *NodeAppService) ReleaseIP(ipID string) error {
	ip, err := s.ipRepo.GetByID(ipID)
	if err != nil {
		return err
	}
	ip.Release()
	return s.ipRepo.Save(ip)
}

// ──────────────────────────────────────────────────────────────────────
// Region CRUD (absorbed from former RegionAppService)
// ──────────────────────────────────────────────────────────────────────

func (s *NodeAppService) CreateRegion(code, name, flagIcon string) (*domain.Region, error) {
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

func (s *NodeAppService) GetRegion(id string) (*domain.Region, error) {
	return s.regionRepo.GetByID(id)
}

func (s *NodeAppService) ListRegions() ([]*domain.Region, error) {
	return s.regionRepo.ListAll()
}

func (s *NodeAppService) ListActiveRegions() ([]*domain.Region, error) {
	return s.regionRepo.ListActive()
}

func (s *NodeAppService) ActivateRegion(id string) error {
	r, err := s.regionRepo.GetByID(id)
	if err != nil {
		return err
	}
	r.Activate()
	return s.regionRepo.Save(r)
}

func (s *NodeAppService) DeactivateRegion(id string) error {
	r, err := s.regionRepo.GetByID(id)
	if err != nil {
		return err
	}
	r.Deactivate()
	return s.regionRepo.Save(r)
}

// ──────────────────────────────────────────────────────────────────────
// Resource Pool CRUD
// ──────────────────────────────────────────────────────────────────────

func (s *NodeAppService) CreateResourcePool(name, regionID string) (*domain.ResourcePool, error) {
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

func (s *NodeAppService) GetResourcePool(poolID string) (*domain.ResourcePool, error) {
	pool, err := s.poolRepo.GetByID(poolID)
	if err != nil {
		return nil, err
	}
	// Attach nodes for the capacity view
	nodes, err := s.hostRepo.ListEnabledByResourcePoolID(poolID)
	if err != nil {
		return pool, nil // return pool without nodes on error
	}
	pool.WithNodes(nodes)
	return pool, nil
}

func (s *NodeAppService) ListResourcePools() ([]*domain.ResourcePool, error) {
	return s.poolRepo.ListAll()
}

func (s *NodeAppService) ListActiveResourcePools() ([]*domain.ResourcePool, error) {
	return s.poolRepo.ListActive()
}

func (s *NodeAppService) UpdateResourcePool(poolID, name, regionID string) (*domain.ResourcePool, error) {
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

func (s *NodeAppService) ActivateResourcePool(id string) error {
	pool, err := s.poolRepo.GetByID(id)
	if err != nil {
		return err
	}
	pool.Activate()
	return s.poolRepo.Save(pool)
}

func (s *NodeAppService) DeactivateResourcePool(id string) error {
	pool, err := s.poolRepo.GetByID(id)
	if err != nil {
		return err
	}
	pool.Deactivate()
	return s.poolRepo.Save(pool)
}

// AssignNodeToPool binds a host node to a resource pool.
func (s *NodeAppService) AssignNodeToPool(nodeID, poolID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	// Verify pool exists
	if _, err := s.poolRepo.GetByID(poolID); err != nil {
		return err
	}
	h.SetResourcePoolID(poolID)
	return s.hostRepo.Save(h)
}

// RemoveNodeFromPool unbinds a host node from its resource pool.
func (s *NodeAppService) RemoveNodeFromPool(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	h.SetResourcePoolID("")
	return s.hostRepo.Save(h)
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

// ListPoolCapacities returns a capacity summary for every active resource pool.
func (s *NodeAppService) ListPoolCapacities() ([]PoolCapacitySummary, error) {
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

// ──────────────────────────────────────────────────────────────────────
// Node Token Authentication (used by gRPC interceptor)
// ──────────────────────────────────────────────────────────────────────

// ValidateNodeToken looks up a host node by its permanent token.
// Returns the node ID if valid, or an error if the token is unknown/revoked.
func (s *NodeAppService) ValidateNodeToken(token string) (string, error) {
	h, err := s.hostRepo.GetByNodeToken(token)
	if err != nil {
		return "", errors.New("app_error: invalid node token")
	}
	return h.ID(), nil
}

// RevokeNodeToken clears the permanent node token, forcing the agent to re-bootstrap.
func (s *NodeAppService) RevokeNodeToken(nodeID string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	h.RevokeNodeToken()
	return s.hostRepo.Save(h)
}

// ──────────────────────────────────────────────────────────────────────
// Bootstrap Token Management (admin API)
// ──────────────────────────────────────────────────────────────────────

// CreateBootstrapToken creates a new one-time-use bootstrap token bound to a specific node.
func (s *NodeAppService) CreateBootstrapToken(nodeID string, ttl time.Duration, description string) (*domain.BootstrapToken, error) {
	// Verify the target node exists
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

// ListBootstrapTokens returns all bootstrap tokens (for admin dashboard).
func (s *NodeAppService) ListBootstrapTokens() ([]*domain.BootstrapToken, error) {
	return s.btRepo.ListAll()
}

// RevokeBootstrapToken deletes a bootstrap token so it can no longer be used.
func (s *NodeAppService) RevokeBootstrapToken(id string) error {
	return s.btRepo.Delete(id)
}
