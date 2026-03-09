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

// ProvisioningEventHandler subscribes to domain events published by the
// Product bounded context and triggers physical provisioning on the Node side.
//
// This is the event-driven bridge: Product.PurchaseProduct() publishes
// ProductPurchasedEvent → this handler picks a node from the resource pool,
// allocates a physical slot, and enqueues a provisioning task for the agent.
type ProvisioningEventHandler struct {
	hostRepo domain.HostNodeRepository
	poolRepo domain.ResourcePoolRepository
	taskRepo domain.TaskRepository
	ids      IDGenerator
}

func NewProvisioningEventHandler(
	hostRepo domain.HostNodeRepository,
	poolRepo domain.ResourcePoolRepository,
	taskRepo domain.TaskRepository,
	ids IDGenerator,
) *ProvisioningEventHandler {
	return &ProvisioningEventHandler{
		hostRepo: hostRepo,
		poolRepo: poolRepo,
		taskRepo: taskRepo,
		ids:      ids,
	}
}

// Register wires up all event subscriptions on the given EventBus.
func (h *ProvisioningEventHandler) Register(bus *eventbus.EventBus) {
	bus.Subscribe("product.purchased", h.onProductPurchased)
	bus.Subscribe("product.slot_released", h.onProductSlotReleased)
}

// onProductPurchased handles the ProductPurchasedEvent:
//  1. Builds a ResourcePool from the pool's enabled nodes.
//  2. Selects the least-loaded node (load balancing).
//  3. Allocates a physical slot on that node.
//  4. Enqueues a provisioning task for the agent.
func (h *ProvisioningEventHandler) onProductPurchased(evt eventbus.Event) {
	e, ok := evt.(events.ProductPurchasedEvent)
	if !ok {
		return
	}

	log.Printf("[provisioning] handling ProductPurchasedEvent: product=%s region=%s customer=%s order=%s",
		e.ProductID, e.RegionID, e.CustomerID, e.OrderID)

	// 1. Build the resource pool — try ResourcePoolID first, fall back to RegionID
	pool, err := h.buildPool(e.ResourcePoolID, e.RegionID)
	if err != nil {
		log.Printf("[provisioning] WARNING: could not build resource pool: %v — task will be queued without node assignment", err)
		h.enqueuePendingTask(e, "")
		return
	}

	// 2. Select the best node (least-loaded with capacity)
	node, err := pool.SelectNode()
	if err != nil {
		log.Printf("[provisioning] WARNING: no available nodes in pool %s — task queued for pending provisioning", pool.Name())
		h.enqueuePendingTask(e, "")
		return
	}

	// 3. Allocate a physical slot on the selected node
	if err := node.AllocateSlot(); err != nil {
		log.Printf("[provisioning] WARNING: failed to allocate slot on node %s: %v — task queued for pending provisioning", node.Code(), err)
		h.enqueuePendingTask(e, "")
		return
	}
	if err := h.hostRepo.Save(node); err != nil {
		log.Printf("[provisioning] ERROR: failed to save node %s after slot allocation: %v", node.Code(), err)
		return
	}

	// 4. Enqueue a provisioning task for the agent
	instanceID := h.ids.NewID()
	task := &contracts.Task{
		ID:     h.ids.NewID(),
		NodeID: node.ID(),
		Type:   contracts.TaskProvision,
		Status: contracts.TaskStatusQueued,
		Spec: contracts.ProvisionSpec{
			InstanceID: instanceID,
			Hostname:   e.Hostname,
			OS:         e.OS,
			CPU:        e.CPU,
			MemoryMB:   e.MemoryMB,
			DiskGB:     e.DiskGB,
			VirtType:   contracts.VirtKVM, // default to KVM
		},
		CreatedAt: timeNowRFC3339(),
	}
	if err := h.taskRepo.Save(task); err != nil {
		log.Printf("[provisioning] ERROR: failed to enqueue task for node %s: %v", node.Code(), err)
		return
	}

	log.Printf("[provisioning] SUCCESS: provisioning task %s enqueued on node %s (pool %s) for order %s",
		task.ID, node.Code(), pool.Name(), e.OrderID)

	// ── MVP MOCK: auto-complete the task since there is no real agent ──
	// When a real agent is connected, remove this block — the agent will
	// pick up the task via heartbeat and report completion.
	h.mockCompleteTask(task, node.ID())
}

// mockCompleteTask immediately marks a provisioning task as completed and
// publishes a ProvisioningCompletedEvent. This simulates what the agent
// would do after actually creating a VM. Remove this when real agents are
// integrated.
func (h *ProvisioningEventHandler) mockCompleteTask(task *contracts.Task, nodeID string) {
	task.Status = contracts.TaskStatusCompleted
	task.FinishedAt = timeNowRFC3339()
	if err := h.taskRepo.Save(task); err != nil {
		log.Printf("[provisioning-mock] ERROR: failed to mark task %s as completed: %v", task.ID, err)
		return
	}
	log.Printf("[provisioning-mock] task %s auto-completed (mock mode, no real agent)", task.ID)
}

// onProductSlotReleased handles cancellation/termination — could release
// physical slots and process pending queue. Placeholder for future expansion.
func (h *ProvisioningEventHandler) onProductSlotReleased(evt eventbus.Event) {
	e, ok := evt.(events.ProductSlotReleasedEvent)
	if !ok {
		return
	}
	log.Printf("[provisioning] handling ProductSlotReleasedEvent: product=%s region=%s order=%s",
		e.ProductID, e.RegionID, e.OrderID)
}

// buildPool constructs a ResourcePool with its enabled nodes.
// It tries the explicit pool ID first, then falls back to finding a pool by region.
func (h *ProvisioningEventHandler) buildPool(poolID, regionID string) (*domain.ResourcePool, error) {
	if poolID != "" {
		pool, err := h.poolRepo.GetByID(poolID)
		if err != nil {
			return nil, err
		}
		nodes, err := h.hostRepo.ListEnabledByResourcePoolID(poolID)
		if err != nil {
			return nil, err
		}
		pool.WithNodes(nodes)
		return pool, nil
	}

	// Fallback: find pools by regionID and use the first active one
	if regionID != "" {
		pools, err := h.poolRepo.GetByRegionID(regionID)
		if err != nil {
			return nil, err
		}
		for _, pool := range pools {
			if pool.IsActive() {
				nodes, err := h.hostRepo.ListEnabledByResourcePoolID(pool.ID())
				if err != nil {
					continue
				}
				pool.WithNodes(nodes)
				return pool, nil
			}
		}
	}

	return nil, errors.New("provisioning: no resource pool found for provisioning")
}

// enqueuePendingTask creates a task without a node assignment (pending provisioning queue).
func (h *ProvisioningEventHandler) enqueuePendingTask(e events.ProductPurchasedEvent, nodeID string) {
	instanceID := h.ids.NewID()
	task := &contracts.Task{
		ID:     h.ids.NewID(),
		NodeID: nodeID, // empty string = not yet assigned to a node
		Type:   contracts.TaskProvision,
		Status: contracts.TaskStatusQueued,
		Spec: contracts.ProvisionSpec{
			InstanceID: instanceID,
			Hostname:   e.Hostname,
			OS:         e.OS,
			CPU:        e.CPU,
			MemoryMB:   e.MemoryMB,
			DiskGB:     e.DiskGB,
			VirtType:   contracts.VirtKVM,
		},
		CreatedAt: timeNowRFC3339(),
	}
	if err := h.taskRepo.Save(task); err != nil {
		log.Printf("[provisioning] ERROR: failed to enqueue pending task: %v", err)
	}
}

func timeNowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}
