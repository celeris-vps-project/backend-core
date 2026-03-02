package app

import (
	"backend-core/internal/instance/domain"
	"time"
)

type IDGenerator interface {
	NewID() string
}

type InstanceAppService struct {
	nodeRepo     domain.NodeRepository
	instanceRepo domain.InstanceRepository
	ids          IDGenerator
}

func NewInstanceAppService(
	nodeRepo domain.NodeRepository,
	instanceRepo domain.InstanceRepository,
	ids IDGenerator,
) *InstanceAppService {
	return &InstanceAppService{nodeRepo: nodeRepo, instanceRepo: instanceRepo, ids: ids}
}

// ---- Node operations (admin) ----

func (s *InstanceAppService) CreateNode(code, location, name string, totalSlots int) (*domain.Node, error) {
	id := s.ids.NewID()
	node, err := domain.NewNode(id, code, location, name, totalSlots)
	if err != nil {
		return nil, err
	}
	if err := s.nodeRepo.Save(node); err != nil {
		return nil, err
	}
	return node, nil
}

func (s *InstanceAppService) GetNode(nodeID string) (*domain.Node, error) {
	return s.nodeRepo.GetByID(nodeID)
}

// ListNodes returns all nodes.
func (s *InstanceAppService) ListNodes() ([]*domain.Node, error) {
	return s.nodeRepo.ListAll()
}

// ListNodesByLocation returns nodes grouped by location code (e.g. "DE-fra").
func (s *InstanceAppService) ListNodesByLocation(location string) ([]*domain.Node, error) {
	return s.nodeRepo.ListByLocation(location)
}

// ---- Instance purchase ----

// PurchaseInstance allocates a slot on the given node and creates a pending instance.
func (s *InstanceAppService) PurchaseInstance(
	customerID, orderID, nodeID string,
	hostname, plan, os string,
	cpu, memoryMB, diskGB int,
) (*domain.Instance, error) {
	// 1. Load and validate node capacity
	node, err := s.nodeRepo.GetByID(nodeID)
	if err != nil {
		return nil, err
	}
	if err := node.AllocateSlot(); err != nil {
		return nil, err
	}

	// 2. Create the instance
	id := s.ids.NewID()
	inst, err := domain.NewInstance(id, customerID, orderID, nodeID, hostname, plan, os, cpu, memoryMB, diskGB)
	if err != nil {
		return nil, err
	}

	// 3. Persist both
	if err := s.nodeRepo.Save(node); err != nil {
		return nil, err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// ---- Instance queries ----

func (s *InstanceAppService) GetInstance(instanceID string) (*domain.Instance, error) {
	return s.instanceRepo.GetByID(instanceID)
}

func (s *InstanceAppService) ListByCustomer(customerID string) ([]*domain.Instance, error) {
	return s.instanceRepo.ListByCustomerID(customerID)
}

func (s *InstanceAppService) ListByNode(nodeID string) ([]*domain.Instance, error) {
	return s.instanceRepo.ListByNodeID(nodeID)
}

// ---- Instance lifecycle ----

func (s *InstanceAppService) StartInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.Start(time.Now()); err != nil {
		return err
	}
	return s.instanceRepo.Save(inst)
}

func (s *InstanceAppService) StopInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.Stop(time.Now()); err != nil {
		return err
	}
	return s.instanceRepo.Save(inst)
}

func (s *InstanceAppService) SuspendInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.Suspend(time.Now()); err != nil {
		return err
	}
	return s.instanceRepo.Save(inst)
}

func (s *InstanceAppService) UnsuspendInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.Unsuspend(time.Now()); err != nil {
		return err
	}
	return s.instanceRepo.Save(inst)
}

func (s *InstanceAppService) TerminateInstance(instanceID string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.Terminate(time.Now()); err != nil {
		return err
	}

	// Release the node slot
	node, err := s.nodeRepo.GetByID(inst.NodeID())
	if err == nil {
		_ = node.ReleaseSlot()
		_ = s.nodeRepo.Save(node)
	}

	return s.instanceRepo.Save(inst)
}

func (s *InstanceAppService) AssignIP(instanceID, ipv4, ipv6 string) error {
	inst, err := s.instanceRepo.GetByID(instanceID)
	if err != nil {
		return err
	}
	if err := inst.AssignIP(ipv4, ipv6); err != nil {
		return err
	}
	return s.instanceRepo.Save(inst)
}

// ---- Node admin ----

func (s *InstanceAppService) EnableNode(nodeID string) error {
	node, err := s.nodeRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	node.Enable()
	return s.nodeRepo.Save(node)
}

func (s *InstanceAppService) DisableNode(nodeID string) error {
	node, err := s.nodeRepo.GetByID(nodeID)
	if err != nil {
		return err
	}
	node.Disable()
	return s.nodeRepo.Save(node)
}

// AvailableLocations returns a deduplicated list of locations that have capacity.
func (s *InstanceAppService) AvailableLocations() ([]LocationSummary, error) {
	nodes, err := s.nodeRepo.ListAll()
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
