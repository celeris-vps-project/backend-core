package app

import (
	"backend-core/internal/node/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"log"
	"time"
)

// ProvisioningEventHandler subscribes to domain events published by the
// Product bounded context and triggers physical provisioning on the Node side.
//
// This is the event-driven bridge: Product.PurchaseProduct() publishes
// ProductPurchasedEvent → this handler picks a node from the region pool,
// allocates a physical slot, and enqueues a provisioning task for the agent.
type ProvisioningEventHandler struct {
	hostRepo   domain.HostNodeRepository
	regionRepo domain.RegionRepository
	taskRepo   domain.TaskRepository
	ids        IDGenerator
}

func NewProvisioningEventHandler(
	hostRepo domain.HostNodeRepository,
	regionRepo domain.RegionRepository,
	taskRepo domain.TaskRepository,
	ids IDGenerator,
) *ProvisioningEventHandler {
	return &ProvisioningEventHandler{
		hostRepo:   hostRepo,
		regionRepo: regionRepo,
		taskRepo:   taskRepo,
		ids:        ids,
	}
}

// Register wires up all event subscriptions on the given EventBus.
func (h *ProvisioningEventHandler) Register(bus *eventbus.EventBus) {
	bus.Subscribe("product.purchased", h.onProductPurchased)
	bus.Subscribe("product.slot_released", h.onProductSlotReleased)
}

// onProductPurchased handles the ProductPurchasedEvent:
//  1. Builds a ResourcePool from the region's enabled nodes.
//  2. Selects the least-loaded node (load balancing).
//  3. Allocates a physical slot on that node.
//  4. Enqueues a provisioning task for the agent.
//
// If no physical capacity is available, the task is still created with a
// "queued" status — it enters the pending-provisioning queue and will be
// picked up when capacity becomes available (or an admin adds nodes).
func (h *ProvisioningEventHandler) onProductPurchased(evt eventbus.Event) {
	e, ok := evt.(events.ProductPurchasedEvent)
	if !ok {
		return
	}

	log.Printf("[provisioning] handling ProductPurchasedEvent: product=%s region=%s customer=%s order=%s",
		e.ProductID, e.RegionID, e.CustomerID, e.OrderID)

	// 1. Build the resource pool for the target region
	pool, err := h.buildPool(e.RegionID)
	if err != nil {
		log.Printf("[provisioning] WARNING: could not build resource pool for region %s: %v — task will be queued without node assignment", e.RegionID, err)
		h.enqueuePendingTask(e, "")
		return
	}

	// 2. Select the best node (least-loaded with capacity)
	node, err := pool.SelectNode()
	if err != nil {
		// No capacity — queue for later provisioning (pending-provisioning queue)
		log.Printf("[provisioning] WARNING: no available nodes in region %s — task queued for pending provisioning", e.RegionID)
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

	log.Printf("[provisioning] SUCCESS: provisioning task %s enqueued on node %s (region %s) for order %s",
		task.ID, node.Code(), e.RegionID, e.OrderID)
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
	// TODO: find the instance for this order, release the node slot,
	// and check if there are pending tasks that can now be fulfilled.
}

// buildPool constructs a ResourcePool for the given region.
func (h *ProvisioningEventHandler) buildPool(regionID string) (*domain.ResourcePool, error) {
	region, err := h.regionRepo.GetByID(regionID)
	if err != nil {
		return nil, err
	}
	nodes, err := h.hostRepo.ListEnabledByRegionID(regionID)
	if err != nil {
		return nil, err
	}
	return domain.NewResourcePool(region.ID(), region.Code(), region.Name(), nodes), nil
}

// enqueuePendingTask creates a task without a node assignment (pending provisioning queue).
// These tasks can be retried when new capacity is added to the pool.
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
