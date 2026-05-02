package app

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"errors"
	"log"
	"sort"
	"strings"
	"time"
)

type IDGenerator interface{ NewID() string }

const (
	DefaultNATPortStart = 20000
	DefaultNATPortEnd   = 65535
	DefaultNATBridge    = "vmbr2"
)

// ProvisioningAppService manages all provisioning-layer concerns:
// host nodes, IP pools, resource pools, regions, agent registration,
// bootstrap tokens, tasks, and capacity queries.
type ProvisioningAppService struct {
	hostRepo    domain.HostNodeRepository
	ipRepo      domain.IPAddressRepository
	natPortRepo domain.NATPortAllocationRepository
	taskRepo    domain.TaskRepository
	regionRepo  domain.RegionRepository
	poolRepo    domain.ResourcePoolRepository
	btRepo      domain.BootstrapTokenRepository
	stateCache  domain.NodeStateCache
	ids         IDGenerator
	bus         *eventbus.EventBus
}

func NewProvisioningAppService(
	hostRepo domain.HostNodeRepository,
	ipRepo domain.IPAddressRepository,
	natPortRepo domain.NATPortAllocationRepository,
	taskRepo domain.TaskRepository,
	regionRepo domain.RegionRepository,
	poolRepo domain.ResourcePoolRepository,
	btRepo domain.BootstrapTokenRepository,
	stateCache domain.NodeStateCache,
	ids IDGenerator,
	bus *eventbus.EventBus,
) *ProvisioningAppService {
	return &ProvisioningAppService{
		hostRepo: hostRepo, ipRepo: ipRepo, natPortRepo: natPortRepo, taskRepo: taskRepo,
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

func (s *ProvisioningAppService) CreateHost(code, location, name, secret string, totalSlots, natPortStart, natPortEnd int, natBridge, natEntryHost string) (*domain.HostNode, error) {
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
	natPortStart, natPortEnd = defaultNATPortRange(natPortStart, natPortEnd)
	if err := h.SetNATPortRange(natPortStart, natPortEnd); err != nil {
		return nil, err
	}
	if err := h.SetNATBridge(defaultNATBridge(natBridge)); err != nil {
		return nil, err
	}
	if err := h.SetNATEntryHost(natEntryHost); err != nil {
		return nil, err
	}
	if err := s.hostRepo.Save(h); err != nil {
		return nil, err
	}
	return h, nil
}

func defaultNATPortRange(start, end int) (int, int) {
	if start == 0 {
		start = DefaultNATPortStart
	}
	if end == 0 {
		end = DefaultNATPortEnd
	}
	return start, end
}

func defaultNATBridge(bridge string) string {
	bridge = strings.TrimSpace(bridge)
	if bridge == "" {
		return DefaultNATBridge
	}
	return bridge
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

func (s *ProvisioningAppService) UpdateNATEntryHost(nodeID, natEntryHost string) error {
	h, err := s.hostRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	if err := h.SetNATEntryHost(natEntryHost); err != nil {
		return err
	}
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
		return &contracts.HeartbeatAck{OK: true, NATForwards: s.activeNATForwards(hb.NodeID)}, nil
	}
	return &contracts.HeartbeatAck{OK: true, Tasks: tasks, NATForwards: s.activeNATForwards(hb.NodeID)}, nil
}

func (s *ProvisioningAppService) activeNATForwards(nodeID string) []contracts.NATForwardRule {
	rules := make([]contracts.NATForwardRule, 0)
	usedHostPorts := make(map[int]struct{})
	if s.natPortRepo != nil {
		allocations, err := s.natPortRepo.ListByNodeID(nodeID)
		if err != nil {
			log.Printf("[provisioning] WARNING: failed to list NAT port allocations for node %s: %v", nodeID, err)
		} else {
			for _, allocation := range allocations {
				if allocation == nil || allocation.HostPort() <= 0 || allocation.GuestIP() == "" {
					continue
				}
				usedHostPorts[allocation.HostPort()] = struct{}{}
				rules = append(rules, contracts.NATForwardRule{
					InstanceID: allocation.InstanceID(),
					HostPort:   allocation.HostPort(),
					GuestIP:    allocation.GuestIP(),
					GuestPort:  allocation.GuestPort(),
					Protocol:   allocation.Protocol(),
				})
			}
		}
	}
	if s.ipRepo == nil {
		return rules
	}
	ips, err := s.ipRepo.ListByNodeID(nodeID)
	if err != nil {
		log.Printf("[provisioning] WARNING: failed to list NAT forwards for node %s: %v", nodeID, err)
		return rules
	}
	for _, ip := range ips {
		if ip == nil || !ip.IsNAT() || ip.IsAvailable() || ip.Port() <= 0 || ip.Address() == "" {
			continue
		}
		if _, exists := usedHostPorts[ip.Port()]; exists {
			continue
		}
		usedHostPorts[ip.Port()] = struct{}{}
		rules = append(rules, contracts.NATForwardRule{
			InstanceID: ip.InstanceID(),
			HostPort:   ip.Port(),
			GuestIP:    ip.Address(),
			GuestPort:  domain.DefaultSSHPort,
			Protocol:   domain.NATProtocolTCP,
		})
	}
	return rules
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
	if err := s.taskRepo.Save(task); err != nil {
		return err
	}

	// Emit provisioning events based on task outcome.
	// The Instance domain subscribes to these events to update instance state.
	if s.bus != nil {
		switch task.Type {
		case contracts.TaskProvision:
			switch result.Status {
			case contracts.TaskStatusCompleted:
				ipv4 := result.IPv4
				if ipv4 == "" {
					ipv4 = task.Spec.IPv4
				}
				ipv6 := result.IPv6
				if ipv6 == "" {
					ipv6 = task.Spec.IPv6
				}
				// Resolve NAT info from the task spec
				networkMode := string(task.Spec.NetworkMode)
				natPort := task.Spec.NATPort
				natForwards := task.Spec.NATForwards
				if len(natForwards) == 0 && natPort > 0 {
					natForwards = []contracts.NATForwardRule{{
						InstanceID: task.Spec.InstanceID,
						HostPort:   natPort,
						GuestIP:    ipv4,
						GuestPort:  domain.DefaultSSHPort,
						Protocol:   domain.NATProtocolTCP,
					}}
				}
				hostIP := ""
				if networkMode == "nat" {
					hostIP = s.resolveHostIP(task.NodeID)
				}

				s.bus.Publish(events.ProvisioningCompletedEvent{
					InstanceID:  task.Spec.InstanceID,
					NodeID:      task.NodeID,
					TaskID:      task.ID,
					IPv4:        ipv4,
					IPv6:        ipv6,
					VMState:     result.VMState,
					NetworkMode: networkMode,
					NATPort:     natPort,
					NATForwards: contractNATForwardsToEvents(natForwards),
					HostIP:      hostIP,
				})
				log.Printf("[provisioning] task %s completed: instance=%s ipv4=%s vm_state=%s nat_port=%d",
					task.ID, task.Spec.InstanceID, ipv4, result.VMState, natPort)

			case contracts.TaskStatusFailed:
				s.bus.Publish(events.ProvisioningFailedEvent{
					InstanceID: task.Spec.InstanceID,
					NodeID:     task.NodeID,
					TaskID:     task.ID,
					Error:      result.Error,
				})
				log.Printf("[provisioning] task %s failed: instance=%s error=%s",
					task.ID, task.Spec.InstanceID, result.Error)
			}
		default:
			switch result.Status {
			case contracts.TaskStatusCompleted:
				s.bus.Publish(events.InstanceTaskCompletedEvent{
					InstanceID: task.Spec.InstanceID,
					NodeID:     task.NodeID,
					TaskID:     task.ID,
					TaskType:   string(task.Type),
					IPv4:       result.IPv4,
					IPv6:       result.IPv6,
					VMState:    result.VMState,
				})
			case contracts.TaskStatusFailed:
				s.bus.Publish(events.InstanceTaskFailedEvent{
					InstanceID: task.Spec.InstanceID,
					NodeID:     task.NodeID,
					TaskID:     task.ID,
					TaskType:   string(task.Type),
					Error:      result.Error,
				})
			}
		}
	}

	return nil
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

	if s.natPortRepo == nil {
		return s.allocateLegacyNATPort(node, nodeID, instanceID)
	}

	// 1. Try to reuse a previously released NAT guest allocation.
	existing, findErr := s.ipRepo.FindAvailableNAT(nodeID)
	if findErr == nil && existing != nil {
		if existing.Address() == "" {
			allocations, err := s.ipRepo.ListByNodeID(nodeID)
			if err != nil {
				return "", 0, err
			}
			guestIP, err := nextNATGuestIPv4(node, allocations)
			if err != nil {
				return "", 0, err
			}
			if err := existing.SetAddress(guestIP); err != nil {
				return "", 0, err
			}
		}
		if err := existing.Assign(instanceID); err != nil {
			return "", 0, err
		}
		if err := s.ipRepo.Save(existing); err != nil {
			return "", 0, err
		}
		forwards, err := s.createNATPortAllocations(node, instanceID, existing.Address(), 1)
		if err != nil {
			existing.Release()
			_ = s.ipRepo.Save(existing)
			return "", 0, err
		}
		if err := existing.SetPort(forwards[0].HostPort); err != nil {
			existing.Release()
			_ = s.ipRepo.Save(existing)
			_ = s.natPortRepo.DeleteByInstanceID(instanceID)
			return "", 0, err
		}
		if err := s.ipRepo.Save(existing); err != nil {
			_ = s.natPortRepo.DeleteByInstanceID(instanceID)
			return "", 0, err
		}
		hostIP = s.resolveHostIP(nodeID)
		return hostIP, forwards[0].HostPort, nil
	}

	// 2. Find an internal guest IP and create a single host-port mapping.
	allocations, err := s.ipRepo.ListByNodeID(nodeID)
	if err != nil {
		return "", 0, err
	}
	guestIP, err := nextNATGuestIPv4(node, allocations)
	if err != nil {
		return "", 0, err
	}
	forwards, err := s.createNATPortAllocations(node, instanceID, guestIP, 1)
	if err != nil {
		return "", 0, err
	}

	// 3. Create and assign a new NAT port allocation
	alloc, err := domain.NewNATPortAllocation(s.ids.NewID(), nodeID, guestIP, forwards[0].HostPort)
	if err != nil {
		_ = s.natPortRepo.DeleteByInstanceID(instanceID)
		return "", 0, err
	}
	if err := alloc.Assign(instanceID); err != nil {
		_ = s.natPortRepo.DeleteByInstanceID(instanceID)
		return "", 0, err
	}
	if err := s.ipRepo.Save(alloc); err != nil {
		_ = s.natPortRepo.DeleteByInstanceID(instanceID)
		return "", 0, err
	}

	hostIP = s.resolveHostIP(nodeID)
	return hostIP, forwards[0].HostPort, nil
}

// ReleaseNATPort releases a NAT port allocation back to the pool.
func (s *ProvisioningAppService) ReleaseNATPort(ipID string) error {
	ip, err := s.ipRepo.GetByID(ipID)
	if err != nil {
		return err
	}
	if s.natPortRepo != nil && ip.InstanceID() != "" {
		if err := s.natPortRepo.DeleteByInstanceID(ip.InstanceID()); err != nil {
			return err
		}
	}
	ip.Release()
	return s.ipRepo.Save(ip)
}

// ListNATPorts returns all NAT port allocations on a node.
func (s *ProvisioningAppService) ListNATPorts(nodeID string) ([]int, error) {
	ports := make([]int, 0)
	seen := make(map[int]struct{})
	if s.natPortRepo != nil {
		allocations, err := s.natPortRepo.ListByNodeID(nodeID)
		if err != nil {
			return nil, err
		}
		for _, allocation := range allocations {
			if allocation != nil && allocation.HostPort() > 0 {
				seen[allocation.HostPort()] = struct{}{}
				ports = append(ports, allocation.HostPort())
			}
		}
	}
	if s.ipRepo == nil {
		return ports, nil
	}
	legacyPorts, err := s.ipRepo.ListNATPortsByNodeID(nodeID)
	if err != nil {
		return nil, err
	}
	for _, port := range legacyPorts {
		if port <= 0 {
			continue
		}
		if _, exists := seen[port]; exists {
			continue
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports, nil
}

func (s *ProvisioningAppService) allocateLegacyNATPort(node *domain.HostNode, nodeID, instanceID string) (hostIP string, port int, err error) {
	existing, findErr := s.ipRepo.FindAvailableNAT(nodeID)
	if findErr == nil && existing != nil {
		if existing.Address() == "" {
			allocations, err := s.ipRepo.ListByNodeID(nodeID)
			if err != nil {
				return "", 0, err
			}
			guestIP, err := nextNATGuestIPv4(node, allocations)
			if err != nil {
				return "", 0, err
			}
			if err := existing.SetAddress(guestIP); err != nil {
				return "", 0, err
			}
		}
		if err := existing.Assign(instanceID); err != nil {
			return "", 0, err
		}
		if err := s.ipRepo.Save(existing); err != nil {
			return "", 0, err
		}
		return s.resolveHostIP(nodeID), existing.Port(), nil
	}

	allocations, err := s.ipRepo.ListByNodeID(nodeID)
	if err != nil {
		return "", 0, err
	}
	usedSet := usedLegacyNATPorts(allocations)
	freePort, err := node.FindFreeNATPort(usedSet)
	if err != nil {
		return "", 0, err
	}
	guestIP, err := nextNATGuestIPv4(node, allocations)
	if err != nil {
		return "", 0, err
	}

	alloc, err := domain.NewNATPortAllocation(s.ids.NewID(), nodeID, guestIP, freePort)
	if err != nil {
		return "", 0, err
	}
	if err := alloc.Assign(instanceID); err != nil {
		return "", 0, err
	}
	if err := s.ipRepo.Save(alloc); err != nil {
		return "", 0, err
	}

	return s.resolveHostIP(nodeID), freePort, nil
}

func usedLegacyNATPorts(allocations []*domain.IPAddress) map[int]struct{} {
	used := make(map[int]struct{}, len(allocations))
	for _, ip := range allocations {
		if ip == nil || !ip.IsNAT() || ip.Port() <= 0 {
			continue
		}
		used[ip.Port()] = struct{}{}
	}
	return used
}

func mergeUsedPorts(dst, src map[int]struct{}) {
	for port := range src {
		if port > 0 {
			dst[port] = struct{}{}
		}
	}
}

func (s *ProvisioningAppService) createNATPortAllocations(node *domain.HostNode, instanceID, guestIP string, count int) ([]contracts.NATForwardRule, error) {
	allocations, err := s.natPortRepo.ListByNodeID(node.ID())
	if err != nil {
		return nil, err
	}
	used := usedNATAllocationPorts(allocations)
	if s.ipRepo != nil {
		legacy, err := s.ipRepo.ListByNodeID(node.ID())
		if err != nil {
			return nil, err
		}
		mergeUsedPorts(used, usedLegacyNATPorts(legacy))
	}
	start, err := findFreeNATPortRange(node, used, count)
	if err != nil {
		return nil, err
	}
	allocationID := s.ids.NewID()
	items := make([]*domain.NATPortAllocation, 0, count)
	for i := 0; i < count; i++ {
		hostPort := start + i
		guestPort := domain.DefaultSSHPort
		if i > 0 {
			guestPort = hostPort
		}
		allocation, err := domain.NewNATPortForwardAllocation(
			s.ids.NewID(),
			allocationID,
			node.ID(),
			instanceID,
			guestIP,
			domain.NATProtocolTCP,
			hostPort,
			guestPort,
		)
		if err != nil {
			return nil, err
		}
		items = append(items, allocation)
	}
	if err := s.natPortRepo.SaveMany(items); err != nil {
		return nil, err
	}
	return natAllocationsToForwardRules(items), nil
}

func contractNATForwardsToEvents(forwards []contracts.NATForwardRule) []events.NATForwardRule {
	if len(forwards) == 0 {
		return nil
	}
	out := make([]events.NATForwardRule, 0, len(forwards))
	for _, forward := range forwards {
		if forward.HostPort <= 0 {
			continue
		}
		guestPort := forward.GuestPort
		if guestPort <= 0 {
			guestPort = domain.DefaultSSHPort
		}
		protocol := forward.Protocol
		if protocol == "" {
			protocol = domain.NATProtocolTCP
		}
		out = append(out, events.NATForwardRule{
			HostPort:  forward.HostPort,
			GuestIP:   forward.GuestIP,
			GuestPort: guestPort,
			Protocol:  protocol,
		})
	}
	return out
}

// resolveHostIP gets the user-facing NAT entry host from persistent node
// configuration, falling back to the agent-reported runtime IP for old nodes.
func (s *ProvisioningAppService) resolveHostIP(nodeID string) string {
	node, err := s.hostRepo.GetByID(nodeID)
	if err == nil && node != nil && node.NATEntryHost() != "" {
		return node.NATEntryHost()
	}
	if s.stateCache == nil {
		return ""
	}
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
