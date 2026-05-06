package app

import (
	"backend-core/internal/instance/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"fmt"
	"time"
)

type IDGenerator interface {
	NewID() string
}

type InstanceAppService struct {
	nodeRepo     domain.NodeAllocatorRepository
	instanceRepo domain.InstanceRepository
	ids          IDGenerator
	bus          domain.ProvisioningBus // nil = no async dispatch
	lifecycle    LifecycleTaskScheduler
	events       EventPublisher
	natPorts     NATPortMappingReader
	runtime      InstanceRuntimeStateReader
}

type EventPublisher interface {
	Publish(eventbus.Event)
}

type LifecycleTaskScheduler interface {
	Enqueue(nodeID string, taskType contracts.TaskType, spec contracts.ProvisionSpec) error
}

type NATPortMappingReader interface {
	ListForwardRulesByInstanceID(instanceID string) ([]contracts.NATForwardRule, error)
}

type InstanceRuntimeStateReader interface {
	GetInstanceRuntimeState(instanceID, nodeID string) (contracts.InstanceRuntimeState, bool)
}

func NewInstanceAppService(
	nodeRepo domain.NodeAllocatorRepository,
	instanceRepo domain.InstanceRepository,
	ids IDGenerator,
	bus domain.ProvisioningBus,
) *InstanceAppService {
	return &InstanceAppService{nodeRepo: nodeRepo, instanceRepo: instanceRepo, ids: ids, bus: bus}
}

func (s *InstanceAppService) SetLifecycleScheduler(scheduler LifecycleTaskScheduler) {
	s.lifecycle = scheduler
}

func (s *InstanceAppService) SetEventPublisher(publisher EventPublisher) {
	s.events = publisher
}

func (s *InstanceAppService) SetNATPortMappingReader(reader NATPortMappingReader) {
	s.natPorts = reader
}

func (s *InstanceAppService) SetRuntimeStateReader(reader InstanceRuntimeStateReader) {
	s.runtime = reader
}

// ---- Instance purchase ----

// PurchaseInstance finds an available node in the given region, allocates a slot,
// and creates a pending instance. The caller passes a region (location code)
// instead of a specific node ID — node selection is an internal concern.
func (s *InstanceAppService) PurchaseInstance(
	customerID, orderID, region string,
	hostname, plan, os string,
	cpu, memoryMB, diskGB, bandwidthGB int,
) (*domain.Instance, error) {
	// 1. Find an available node in the requested region
	nodes, err := s.nodeRepo.ListByLocation(region)
	if err != nil {
		return nil, err
	}
	var node domain.NodeAllocator
	for _, n := range nodes {
		if n.HasCapacity() {
			node = n
			break
		}
	}
	if node == nil {
		return nil, fmt.Errorf("no available nodes in region %s", region)
	}

	// 2. Allocate a slot on the chosen node
	if err := s.nodeRepo.AllocateSlotAtomic(node.ID()); err != nil {
		return nil, err
	}

	// 3. Create the instance
	id := s.ids.NewID()
	inst, err := domain.NewInstance(id, customerID, orderID, node.ID(), hostname, plan, os, "", cpu, memoryMB, diskGB, bandwidthGB)
	if err != nil {
		_ = s.nodeRepo.ReleaseSlotAtomic(node.ID())
		return nil, err
	}
	password, err := generateInitialPassword()
	if err != nil {
		_ = s.nodeRepo.ReleaseSlotAtomic(node.ID())
		return nil, err
	}
	inst.SetInitialPassword(password)

	// 4. Persist the instance. Node capacity was already updated atomically.
	if err := s.instanceRepo.Save(inst); err != nil {
		_ = s.nodeRepo.ReleaseSlotAtomic(node.ID())
		return nil, err
	}
	s.publishState(inst)
	return inst, nil
}

// CreatePendingInstance creates a pending instance that is immediately visible
// to the user.
//
// In the payment flow, the provisioning context owns physical placement. This
// method therefore only persists the user-facing delivery record and leaves
// node selection / slot allocation to provisioning.
func (s *InstanceAppService) CreatePendingInstance(
	customerID, orderID, region string,
	hostname, plan, os, networkMode string,
	cpu, memoryMB, diskGB, bandwidthGB int,
) (*domain.Instance, error) {
	// Region remains part of the request contract even though placement is
	// deferred to provisioning. Keep it in the signature for compatibility.
	_ = region

	// Create the instance with no node assignment yet.
	id := s.ids.NewID()
	inst, err := domain.NewInstance(id, customerID, orderID, "", hostname, plan, os, networkMode, cpu, memoryMB, diskGB, bandwidthGB)
	if err != nil {
		return nil, fmt.Errorf("create_pending: %w", err)
	}
	password, err := generateInitialPassword()
	if err != nil {
		return nil, fmt.Errorf("create_pending: generate password failed: %w", err)
	}
	inst.SetInitialPassword(password)

	if err := s.instanceRepo.Save(inst); err != nil {
		return nil, fmt.Errorf("create_pending: save instance failed: %w", err)
	}
	s.publishState(inst)

	return inst, nil
}

// ---- Instance queries ----

func (s *InstanceAppService) GetInstance(instanceID string) (*domain.Instance, error) {
	return s.instanceRepo.GetByID(instanceID)
}

func (s *InstanceAppService) GetByOrderID(orderID string) (*domain.Instance, error) {
	return s.instanceRepo.GetByOrderID(orderID)
}

func (s *InstanceAppService) ListByCustomer(customerID string) ([]*domain.Instance, error) {
	return s.instanceRepo.ListByCustomerID(customerID)
}

func (s *InstanceAppService) ListAll() ([]*domain.Instance, error) {
	return s.instanceRepo.ListAll()
}

func (s *InstanceAppService) ListByNode(nodeID string) ([]*domain.Instance, error) {
	return s.instanceRepo.ListByNodeID(nodeID)
}

func (s *InstanceAppService) ListNATPortMappings(instanceID string) ([]contracts.NATForwardRule, error) {
	if s.natPorts == nil {
		return nil, nil
	}
	return s.natPorts.ListForwardRulesByInstanceID(instanceID)
}

func (s *InstanceAppService) InstanceStatus(inst *domain.Instance) string {
	if inst == nil {
		return "unknown"
	}
	switch inst.ControlStatus() {
	case domain.InstanceControlStatusProvisioning,
		domain.InstanceControlStatusSuspended,
		domain.InstanceControlStatusTerminated:
		return inst.ControlStatus()
	}
	if state := s.InstanceRuntimeState(inst); state != "" {
		return state
	}
	return domain.InstanceControlStatusActive
}

func (s *InstanceAppService) InstanceRuntimeState(inst *domain.Instance) string {
	state, ok := s.instanceRuntimeState(inst)
	if !ok {
		return ""
	}
	return normalizeRuntimeState(state.State)
}

func (s *InstanceAppService) instanceRuntimeState(inst *domain.Instance) (contracts.InstanceRuntimeState, bool) {
	if s.runtime == nil || inst == nil {
		return contracts.InstanceRuntimeState{}, false
	}
	state, ok := s.runtime.GetInstanceRuntimeState(inst.ID(), inst.NodeID())
	if !ok || state.State == "" {
		return contracts.InstanceRuntimeState{}, false
	}
	state.State = normalizeRuntimeState(state.State)
	return state, true
}

// ---- Instance lifecycle ----

func (s *InstanceAppService) StartInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if inst.ControlStatus() != domain.InstanceControlStatusActive {
		return fmt.Errorf("domain_error: only active instances can be started")
	}
	if s.lifecycle != nil && inst.NodeID() != "" {
		return s.enqueueLifecycleTask(inst, contracts.TaskStart)
	}
	if err := inst.Start(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) StopInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if inst.ControlStatus() != domain.InstanceControlStatusActive {
		return fmt.Errorf("domain_error: only active instances can be stopped")
	}
	if s.lifecycle != nil && inst.NodeID() != "" {
		return s.enqueueLifecycleTask(inst, contracts.TaskStop)
	}
	if err := inst.Stop(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) SuspendInstance(instanceID string) error {
	return s.SuspendInstanceWithReason(instanceID, "")
}

func (s *InstanceAppService) SuspendInstanceWithReason(instanceID, reason string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if inst.ControlStatus() == domain.InstanceControlStatusSuspended {
		if reason == "" || inst.SuspendReason() == reason {
			return nil
		}
		inst.SetSuspendReason(reason)
		if err := s.instanceRepo.Save(inst); err != nil {
			return err
		}
		s.publishState(inst)
		return nil
	}
	if inst.ControlStatus() != domain.InstanceControlStatusActive {
		return fmt.Errorf("domain_error: only active instances can be suspended")
	}
	if s.lifecycle != nil && inst.NodeID() != "" {
		if reason != "" && inst.SuspendReason() == reason {
			return nil
		}
		if s.InstanceRuntimeState(inst) == domain.InstanceStatusStopped {
			inst.SetSuspendReason(reason)
			if err := s.instanceRepo.Save(inst); err != nil {
				return err
			}
			s.publishState(inst)
			return s.confirmSuspendedWithReason(inst.ID(), reason)
		}
		if err := s.enqueueLifecycleTask(inst, contracts.TaskSuspend); err != nil {
			return err
		}
		inst.SetSuspendReason(reason)
		if err := s.instanceRepo.Save(inst); err != nil {
			return err
		}
		s.publishState(inst)
		return nil
	}
	if err := inst.SuspendWithReason(time.Now(), reason); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) UnsuspendInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if inst.ControlStatus() != domain.InstanceControlStatusSuspended {
		return fmt.Errorf("domain_error: only suspended instances can be unsuspended")
	}
	if s.lifecycle != nil && inst.NodeID() != "" {
		return s.enqueueLifecycleTask(inst, contracts.TaskUnsuspend)
	}
	if err := inst.Unsuspend(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) TerminateInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if s.lifecycle != nil && inst.NodeID() != "" {
		if inst.ControlStatus() == domain.InstanceControlStatusTerminated {
			return fmt.Errorf("domain_error: instance already terminated")
		}
		return s.enqueueLifecycleTask(inst, contracts.TaskDeprovision)
	}
	if err := inst.Terminate(time.Now()); err != nil {
		return err
	}

	// Release the node slot
	if inst.NodeID() != "" {
		_ = s.nodeRepo.ReleaseSlotAtomic(inst.NodeID())
	}

	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) AssignIP(instanceID, ipv4, ipv6 string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.AssignIP(ipv4, ipv6); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

// ConfirmProvisioning is called when the provisioning layer confirms that
// a VM has been successfully created and booted. It updates the instance
// status to "running", records the actual node assignment, assigns the
// internal IP, and stores NAT port info.
//
// This method is designed to be called from an event handler subscribing
// to ProvisioningCompletedEvent.
func (s *InstanceAppService) ConfirmProvisioning(instanceID, nodeID, ipv4, ipv6, hostIP, networkMode string, natPort int) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		// Instance may not exist yet if the provisioning was triggered
		// via a different flow (e.g. direct VPS provisioner without
		// creating an instance record first). Log and skip.
		fmt.Printf("[InstanceAppService] WARNING: instance %s not found for provisioning confirmation: %v\n", instanceID, err)
		return nil
	}

	if nodeID != "" && inst.NodeID() != nodeID {
		if assignNodeErr := inst.AssignNode(nodeID); assignNodeErr != nil {
			fmt.Printf("[InstanceAppService] WARNING: failed to assign node to instance %s: %v\n", instanceID, assignNodeErr)
		}
	}

	// Assign IP addresses (if provided)
	if ipv4 != "" || ipv6 != "" {
		if assignErr := inst.AssignIP(ipv4, ipv6); assignErr != nil {
			fmt.Printf("[InstanceAppService] WARNING: failed to assign IP to instance %s: %v\n", instanceID, assignErr)
		}
	}
	if hostIP != "" {
		inst.SetHostIP(hostIP)
	}

	// Set NAT mode info
	if networkMode == "nat" && natPort > 0 {
		if natErr := inst.AssignNAT(natPort); natErr != nil {
			fmt.Printf("[InstanceAppService] WARNING: failed to assign NAT port to instance %s: %v\n", instanceID, natErr)
		}
	}

	if inst.ControlStatus() == domain.InstanceControlStatusProvisioning {
		if activeErr := inst.MarkProvisioned(time.Now()); activeErr != nil {
			fmt.Printf("[InstanceAppService] WARNING: failed to mark instance %s provisioned: %v\n", instanceID, activeErr)
		}
	}

	if err := s.instanceRepo.Save(inst); err != nil {
		return fmt.Errorf("confirm_provisioning: save instance %s failed: %w", instanceID, err)
	}
	s.publishState(inst)

	fmt.Printf("[InstanceAppService] provisioning confirmed: instance=%s control_status=%s ipv4=%s nat=%s:%d\n",
		instanceID, inst.ControlStatus(), ipv4, networkMode, natPort)
	return nil
}

func (s *InstanceAppService) ConfirmStarted(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.Start(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) ConfirmStopped(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.Stop(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) ConfirmSuspended(instanceID string) error {
	return s.confirmSuspendedWithReason(instanceID, "")
}

func (s *InstanceAppService) confirmSuspendedWithReason(instanceID, reason string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if inst.ControlStatus() == domain.InstanceControlStatusSuspended {
		if reason != "" && inst.SuspendReason() != reason {
			inst.SetSuspendReason(reason)
			if err := s.instanceRepo.Save(inst); err != nil {
				return err
			}
			s.publishState(inst)
		}
		return nil
	}
	if reason != "" {
		inst.SetSuspendReason(reason)
	}
	if err := inst.Suspend(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) ConfirmUnsuspended(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if inst.ControlStatus() == domain.InstanceControlStatusActive {
		return nil
	}
	if err := inst.Unsuspend(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) ConfirmTerminated(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if inst.ControlStatus() == domain.InstanceControlStatusTerminated {
		return nil
	}
	if err := inst.Terminate(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) RecoverFromBillingSuspension(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if inst.ControlStatus() == domain.InstanceControlStatusActive {
		return nil
	}
	if inst.SuspendReason() == domain.InstanceSuspendReasonTrafficRunOut {
		return nil
	}
	if err := inst.RecoverFromBillingSuspension(time.Now()); err != nil {
		return err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) enqueueLifecycleTask(inst *domain.Instance, taskType contracts.TaskType) error {
	if s.lifecycle == nil {
		return nil
	}
	spec := contracts.ProvisionSpec{
		InstanceID:      inst.ID(),
		Hostname:        inst.Hostname(),
		OS:              inst.OS(),
		CPU:             inst.CPU(),
		MemoryMB:        inst.MemoryMB(),
		DiskGB:          inst.DiskGB(),
		IPv4:            inst.IPv4(),
		IPv6:            inst.IPv6(),
		InitialPassword: inst.InitialPassword(),
	}
	if inst.NetworkMode() == "nat" {
		spec.NetworkMode = contracts.NetworkModeNAT
		spec.NATPort = inst.NATPort()
		spec.NATForwards, _ = s.ListNATPortMappings(inst.ID())
	}
	return s.lifecycle.Enqueue(inst.NodeID(), taskType, spec)
}

func (s *InstanceAppService) publishState(inst *domain.Instance) {
	if s.events == nil || inst == nil {
		return
	}
	s.events.Publish(s.toInstanceStateEvent(inst))
}

func (s *InstanceAppService) PublishInstanceState(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	s.publishState(inst)
	return nil
}

func (s *InstanceAppService) toInstanceStateEvent(inst *domain.Instance) events.InstanceStateUpdatedEvent {
	runtimeState := s.InstanceRuntimeState(inst)
	return events.InstanceStateUpdatedEvent{
		InstanceID:      inst.ID(),
		CustomerID:      inst.CustomerID(),
		OrderID:         inst.OrderID(),
		NodeID:          inst.NodeID(),
		Hostname:        inst.Hostname(),
		Plan:            inst.Plan(),
		OS:              inst.OS(),
		CPU:             inst.CPU(),
		MemoryMB:        inst.MemoryMB(),
		DiskGB:          inst.DiskGB(),
		BandwidthGB:     inst.BandwidthGB(),
		IPv4:            inst.IPv4(),
		IPv6:            inst.IPv6(),
		HostIP:          inst.HostIP(),
		Status:          s.InstanceStatus(inst),
		ControlStatus:   inst.ControlStatus(),
		SuspendReason:   inst.SuspendReason(),
		RuntimeState:    runtimeState,
		NetworkMode:     inst.NetworkMode(),
		NATPort:         inst.NATPort(),
		NATPorts:        natPortsFromRules(s.mustListNATPortMappings(inst.ID())),
		InitialPassword: inst.InitialPassword(),
		CreatedAt:       inst.CreatedAt().Format(time.RFC3339),
		StartedAt:       formatOptionalTime(inst.StartedAt()),
		StoppedAt:       formatOptionalTime(inst.StoppedAt()),
		SuspendedAt:     formatOptionalTime(inst.SuspendedAt()),
		TerminatedAt:    formatOptionalTime(inst.TerminatedAt()),
	}
}

func (s *InstanceAppService) mustListNATPortMappings(instanceID string) []contracts.NATForwardRule {
	rules, err := s.ListNATPortMappings(instanceID)
	if err != nil {
		return nil
	}
	return rules
}

func natPortsFromRules(rules []contracts.NATForwardRule) []int {
	if len(rules) == 0 {
		return nil
	}
	ports := make([]int, 0, len(rules))
	for _, rule := range rules {
		if rule.HostPort > 0 {
			ports = append(ports, rule.HostPort)
		}
	}
	return ports
}

func formatOptionalTime(ts *time.Time) *string {
	if ts == nil {
		return nil
	}
	formatted := ts.Format(time.RFC3339)
	return &formatted
}

func normalizeRuntimeState(state string) string {
	switch state {
	case domain.InstanceStatusRunning, domain.InstanceStatusStopped, "paused":
		return state
	default:
		return ""
	}
}
