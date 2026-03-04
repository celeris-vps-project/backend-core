package app

import (
	"backend-core/internal/instance/domain"
	"time"
)

type IDGenerator interface {
	NewID() string
}

type InstanceAppService struct {
	nodeRepo     domain.NodeAllocatorRepository
	instanceRepo domain.InstanceRepository
	ids          IDGenerator
}

func NewInstanceAppService(
	nodeRepo domain.NodeAllocatorRepository,
	instanceRepo domain.InstanceRepository,
	ids IDGenerator,
) *InstanceAppService {
	return &InstanceAppService{nodeRepo: nodeRepo, instanceRepo: instanceRepo, ids: ids}
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
