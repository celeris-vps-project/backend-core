package app

import (
	"backend-core/internal/instance/domain"
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
}

func NewInstanceAppService(
	nodeRepo domain.NodeAllocatorRepository,
	instanceRepo domain.InstanceRepository,
	ids IDGenerator,
	bus domain.ProvisioningBus,
) *InstanceAppService {
	return &InstanceAppService{nodeRepo: nodeRepo, instanceRepo: instanceRepo, ids: ids, bus: bus}
}

// ---- Instance purchase ----

// PurchaseInstance finds an available node in the given region, allocates a slot,
// and creates a pending instance. The caller passes a region (location code)
// instead of a specific node ID — node selection is an internal concern.
func (s *InstanceAppService) PurchaseInstance(
	customerID, orderID, region string,
	hostname, plan, os string,
	cpu, memoryMB, diskGB int,
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
	if err := node.AllocateSlot(); err != nil {
		return nil, err
	}

	// 3. Create the instance
	id := s.ids.NewID()
	inst, err := domain.NewInstance(id, customerID, orderID, node.ID(), hostname, plan, os, cpu, memoryMB, diskGB)
	if err != nil {
		return nil, err
	}

	// 4. Persist both
	if err := s.nodeRepo.Save(node); err != nil {
		return nil, err
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// CreatePendingInstance creates a pending instance that is immediately visible
// to the user, and dispatches a provisioning request through the ProvisioningBus.
//
// This is the primary entry point called after a successful payment webhook:
//  1. Find an available node in the requested region → allocate a slot
//  2. Persist the Instance with status=pending → user sees it immediately
//  3. Dispatch a ProvisionRequest to the bus → async provisioning
//
// The bus implementation determines how provisioning is processed:
// in-memory channel (throttled), RabbitMQ, direct call, etc.
func (s *InstanceAppService) CreatePendingInstance(
	customerID, orderID, region string,
	hostname, plan, os string,
	cpu, memoryMB, diskGB int,
) (*domain.Instance, error) {
	// 1. Find an available node in the requested region
	nodes, err := s.nodeRepo.ListByLocation(region)
	if err != nil {
		return nil, fmt.Errorf("create_pending: %w", err)
	}
	var node domain.NodeAllocator
	for _, n := range nodes {
		if n.HasCapacity() {
			node = n
			break
		}
	}
	if node == nil {
		return nil, fmt.Errorf("create_pending: no available nodes in region %s", region)
	}

	// 2. Allocate a slot on the chosen node
	if err := node.AllocateSlot(); err != nil {
		return nil, fmt.Errorf("create_pending: slot allocation failed: %w", err)
	}

	// 3. Create the instance (status = pending)
	id := s.ids.NewID()
	inst, err := domain.NewInstance(id, customerID, orderID, node.ID(), hostname, plan, os, cpu, memoryMB, diskGB)
	if err != nil {
		return nil, fmt.Errorf("create_pending: %w", err)
	}

	// 4. Persist node slot + instance (user sees it immediately)
	if err := s.nodeRepo.Save(node); err != nil {
		return nil, fmt.Errorf("create_pending: save node failed: %w", err)
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return nil, fmt.Errorf("create_pending: save instance failed: %w", err)
	}

	// 5. Dispatch to the provisioning bus (async)
	if s.bus != nil {
		req := domain.ProvisionRequest{
			InstanceID: inst.ID(),
			CustomerID: customerID,
			OrderID:    orderID,
			NodeID:     node.ID(),
			Hostname:   hostname,
			Plan:       plan,
			OS:         os,
			CPU:        cpu,
			MemoryMB:   memoryMB,
			DiskGB:     diskGB,
		}
		if err := s.bus.Dispatch(req); err != nil {
			// Instance is already persisted — provisioning can be retried.
			// Log the error but don't fail the whole operation.
			fmt.Printf("[InstanceAppService] WARNING: bus dispatch failed for instance %s: %v\n", inst.ID(), err)
		}
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
