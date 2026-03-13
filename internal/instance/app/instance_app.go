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
//  1. Find an available node in the requested region → allocate a slot (best-effort)
//  2. Persist the Instance with status=pending → user sees it immediately
//  3. Dispatch a ProvisionRequest to the bus → async provisioning (only if node assigned)
//
// Slot validation failure is non-fatal: if no node is available in the region,
// the instance is still created and persisted with an empty nodeID so the user
// can see the pending record. Provisioning will be handled out-of-band.
//
// The bus implementation determines how provisioning is processed:
// in-memory channel (throttled), RabbitMQ, direct call, etc.
func (s *InstanceAppService) CreatePendingInstance(
	customerID, orderID, region string,
	hostname, plan, os string,
	cpu, memoryMB, diskGB int,
) (*domain.Instance, error) {
	// 1. Attempt to find an available node in the requested region (best-effort).
	//    If none is available, we still create the instance record as pending.
	var node domain.NodeAllocator
	nodes, err := s.nodeRepo.ListByLocation(region)
	if err != nil {
		fmt.Printf("[InstanceAppService] WARNING: could not list nodes for region %s: %v\n", region, err)
	} else {
		for _, n := range nodes {
			if n.HasCapacity() {
				node = n
				break
			}
		}
	}

	// 2. Allocate a physical slot on the chosen node (if one was found).
	nodeID := ""
	if node != nil {
		if err := node.AllocateSlot(); err != nil {
			fmt.Printf("[InstanceAppService] WARNING: slot allocation failed on node %s: %v — instance will be created without a node\n", node.ID(), err)
			node = nil // treat as unavailable; fall through to no-node path
		} else {
			nodeID = node.ID()
		}
	}
	if node == nil {
		fmt.Printf("[InstanceAppService] WARNING: no available nodes in region %s — creating pending instance without node assignment\n", region)
	}

	// 3. Create the instance (status = pending, nodeID may be empty)
	id := s.ids.NewID()
	inst, err := domain.NewInstance(id, customerID, orderID, nodeID, hostname, plan, os, cpu, memoryMB, diskGB)
	if err != nil {
		return nil, fmt.Errorf("create_pending: %w", err)
	}

	// 4. Persist node slot (if allocated) + instance (user sees it immediately)
	if node != nil {
		if err := s.nodeRepo.Save(node); err != nil {
			// Non-fatal: log and continue — instance record is more important.
			fmt.Printf("[InstanceAppService] WARNING: save node slot failed for node %s: %v\n", nodeID, err)
		}
	}
	if err := s.instanceRepo.Save(inst); err != nil {
		return nil, fmt.Errorf("create_pending: save instance failed: %w", err)
	}

	// 5. Dispatch to the provisioning bus (async) — only when a node was assigned.
	if s.bus != nil && nodeID != "" {
		req := domain.ProvisionRequest{
			InstanceID: inst.ID(),
			CustomerID: customerID,
			OrderID:    orderID,
			NodeID:     nodeID,
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
