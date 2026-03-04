package app

import (
	"backend-core/internal/node/domain"
	"backend-core/pkg/contracts"
	"errors"
	"log"
	"time"
)

type IDGenerator interface{ NewID() string }

type NodeAppService struct {
	hostRepo    domain.HostNodeRepository
	ipRepo      domain.IPAddressRepository
	taskRepo    domain.TaskRepository
	regionRepo  domain.RegionRepository
	ids         IDGenerator
	agentSecret string // optional global shared secret for agent authentication
}

func NewNodeAppService(
	hostRepo domain.HostNodeRepository,
	ipRepo domain.IPAddressRepository,
	taskRepo domain.TaskRepository,
	regionRepo domain.RegionRepository,
	ids IDGenerator,
) *NodeAppService {
	return &NodeAppService{hostRepo: hostRepo, ipRepo: ipRepo, taskRepo: taskRepo, regionRepo: regionRepo, ids: ids}
}

// SetAgentSecret configures a global shared secret for agent authentication.
// When set, agents can authenticate with either the per-node secret or this global secret.
func (s *NodeAppService) SetAgentSecret(secret string) {
	s.agentSecret = secret
}

// ---- Host CRUD ----

func (s *NodeAppService) CreateHost(code, location, name, secret string, totalSlots int) (*domain.HostNode, error) {
	id := s.ids.NewID()
	h, err := domain.NewHostNode(id, code, location, name, secret)
	if err != nil {
		return nil, err
	}
	if totalSlots > 0 {
		h.SetTotalSlots(totalSlots)
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

// ---- Host capacity management (merged from old instance/app node ops) ----

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

// ---- Agent registration & heartbeat ----

func (s *NodeAppService) RegisterAgent(reg contracts.AgentRegistration) error {
	// Resolve (or auto-create) the region for this agent's location
	regionID := s.ensureRegion(reg.Location)

	h, err := s.hostRepo.GetByID(reg.NodeID)
	if err != nil {
		// Node does not exist — only allow auto-registration with the global secret
		if s.agentSecret == "" || reg.Secret != s.agentSecret {
			return errors.New("app_error: invalid agent secret")
		}

		// Derive sensible defaults from the registration payload
		code := reg.NodeID
		location := reg.Location
		if location == "" {
			location = "unknown"
		}
		name := reg.Hostname
		if name == "" {
			name = reg.NodeID
		}

		h, err = domain.NewHostNode(reg.NodeID, code, location, name, s.agentSecret)
		if err != nil {
			return err
		}
		h.SetRegionID(regionID)
		h.Register(reg.IP, reg.Version, time.Now())
		return s.hostRepo.Save(h)
	}

	// Node exists — accept either the global shared secret or the per-node secret
	validGlobal := s.agentSecret != "" && reg.Secret == s.agentSecret
	validPerNode := h.ValidateSecret(reg.Secret)
	if !validGlobal && !validPerNode {
		return errors.New("app_error: invalid agent secret")
	}
	h.SetRegionID(regionID)
	h.Register(reg.IP, reg.Version, time.Now())
	return s.hostRepo.Save(h)
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
	h, err := s.hostRepo.GetByID(hb.NodeID)
	if err != nil {
		return nil, err
	}
	h.RecordHeartbeat(hb.CPUUsage, hb.MemUsage, hb.DiskUsage, hb.VMCount, time.Now())
	if err := s.hostRepo.Save(h); err != nil {
		return nil, err
	}

	// Return any queued tasks for this node
	tasks, err := s.taskRepo.ListPendingByNodeID(hb.NodeID)
	if err != nil {
		return &contracts.HeartbeatAck{OK: true}, nil
	}
	return &contracts.HeartbeatAck{OK: true, Tasks: tasks}, nil
}

// ---- Task result callback ----

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

// ---- Enqueue a task (called by instance domain or internally) ----

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

// ---- IP management ----

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

// ---- Resource Pool (Region-based node grouping) ----

// GetResourcePool builds a ResourcePool view for the given region,
// aggregating capacity across all enabled nodes in that region.
func (s *NodeAppService) GetResourcePool(regionID string) (*domain.ResourcePool, error) {
	region, err := s.regionRepo.GetByID(regionID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.hostRepo.ListEnabledByRegionID(regionID)
	if err != nil {
		return nil, err
	}
	return domain.NewResourcePool(region.ID(), region.Code(), region.Name(), nodes), nil
}

// PoolCapacitySummary is a read-model for the admin panel showing physical
// capacity of a region-based resource pool.
type PoolCapacitySummary struct {
	RegionID       string `json:"region_id"`
	RegionCode     string `json:"region_code"`
	RegionName     string `json:"region_name"`
	TotalNodes     int    `json:"total_nodes"`
	EnabledNodes   int    `json:"enabled_nodes"`
	TotalSlots     int    `json:"total_slots"`
	UsedSlots      int    `json:"used_slots"`
	AvailableSlots int    `json:"available_slots"`
}

// ListPoolCapacities returns a capacity summary for every active region.
func (s *NodeAppService) ListPoolCapacities() ([]PoolCapacitySummary, error) {
	regions, err := s.regionRepo.ListActive()
	if err != nil {
		return nil, err
	}
	summaries := make([]PoolCapacitySummary, 0, len(regions))
	for _, r := range regions {
		allNodes, err := s.hostRepo.ListByRegionID(r.ID())
		if err != nil {
			continue
		}
		enabledNodes, err := s.hostRepo.ListEnabledByRegionID(r.ID())
		if err != nil {
			continue
		}
		pool := domain.NewResourcePool(r.ID(), r.Code(), r.Name(), enabledNodes)
		summaries = append(summaries, PoolCapacitySummary{
			RegionID:       r.ID(),
			RegionCode:     r.Code(),
			RegionName:     r.Name(),
			TotalNodes:     len(allNodes),
			EnabledNodes:   len(enabledNodes),
			TotalSlots:     pool.TotalPhysicalSlots(),
			UsedSlots:      pool.UsedPhysicalSlots(),
			AvailableSlots: pool.AvailablePhysicalSlots(),
		})
	}
	return summaries, nil
}
