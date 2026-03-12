package app

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"errors"
	"log"
	"time"
)

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Provisioner --- the generic provisioning interface (Bridge pattern).
//
// Different product types implement this interface to handle their specific
// resource allocation logic. The ProvisionDispatcher routes provisioning
// commands to the correct Provisioner based on product type.
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

// ProvisionCommand carries all the info needed to provision a purchased product.
type ProvisionCommand struct {
	ProductID      string
	ProductSlug    string
	ProductType    string // "vps", "license", "cdn", etc.
	ResourcePoolID string
	RegionID       string
	CustomerID     string
	OrderID        string
	Hostname       string
	OS             string
	CPU            int
	MemoryMB       int
	DiskGB         int
}

// ProvisionResult contains the outcome of a provisioning operation.
type ProvisionResult struct {
	InstanceID string
	NodeID     string
	TaskID     string
	Success    bool
}

// Provisioner is the generic interface for resource provisioning.
// Each product type (VPS, license, CDN, etc.) implements this interface.
type Provisioner interface {
	// Provision allocates resources for a purchased product.
	Provision(cmd ProvisionCommand) (*ProvisionResult, error)
	// Release frees resources when a product is cancelled/terminated.
	Release(cmd ProvisionCommand) error
	// ProductType returns the type of product this provisioner handles.
	ProductType() string
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// ProvisionDispatcher --- routes provisioning commands to the correct handler
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

// ProvisionDispatcher manages a registry of Provisioners and routes
// provisioning commands based on product type.
type ProvisionDispatcher struct {
	provisioners map[string]Provisioner
	defaultType  string // fallback product type (e.g. "vps")
}

// NewProvisionDispatcher creates a dispatcher with an optional default type.
func NewProvisionDispatcher(defaultType string) *ProvisionDispatcher {
	return &ProvisionDispatcher{
		provisioners: make(map[string]Provisioner),
		defaultType:  defaultType,
	}
}

// Register adds a Provisioner for a specific product type.
func (d *ProvisionDispatcher) Register(p Provisioner) {
	d.provisioners[p.ProductType()] = p
	log.Printf("[provision-dispatcher] registered provisioner for type %q", p.ProductType())
}

// Dispatch routes a provisioning command to the appropriate Provisioner.
func (d *ProvisionDispatcher) Dispatch(cmd ProvisionCommand) (*ProvisionResult, error) {
	pt := cmd.ProductType
	if pt == "" {
		pt = d.defaultType
	}
	p, ok := d.provisioners[pt]
	if !ok {
		return nil, errors.New("provision_error: no provisioner registered for product type: " + pt)
	}
	return p.Provision(cmd)
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// VPSProvisioner --- handles VPS-type products using ResourcePool + HostNode
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

// VPSProvisioner implements the Provisioner interface for VPS products.
// It selects a node from the resource pool, allocates a slot, and enqueues
// a provisioning task for the agent.
type VPSProvisioner struct {
	hostRepo domain.HostNodeRepository
	poolRepo domain.ResourcePoolRepository
	taskRepo domain.TaskRepository
	ids      IDGenerator
}

// NewVPSProvisioner creates a provisioner for VPS-type products.
func NewVPSProvisioner(
	hostRepo domain.HostNodeRepository,
	poolRepo domain.ResourcePoolRepository,
	taskRepo domain.TaskRepository,
	ids IDGenerator,
) *VPSProvisioner {
	return &VPSProvisioner{
		hostRepo: hostRepo,
		poolRepo: poolRepo,
		taskRepo: taskRepo,
		ids:      ids,
	}
}

func (p *VPSProvisioner) ProductType() string { return "vps" }

func (p *VPSProvisioner) Provision(cmd ProvisionCommand) (*ProvisionResult, error) {
	log.Printf("[vps-provisioner] provisioning: product=%s pool=%s customer=%s order=%s",
		cmd.ProductID, cmd.ResourcePoolID, cmd.CustomerID, cmd.OrderID)

	// 1. Build the resource pool with nodes
	pool, err := p.buildPool(cmd.ResourcePoolID, cmd.RegionID)
	if err != nil {
		log.Printf("[vps-provisioner] WARNING: could not build resource pool: %v", err)
		p.enqueuePendingTask(cmd, "")
		return &ProvisionResult{Success: false}, nil
	}

	// 2. Select the least-loaded node
	node, err := pool.SelectNode()
	if err != nil {
		log.Printf("[vps-provisioner] WARNING: no available nodes in pool %s", pool.Name())
		p.enqueuePendingTask(cmd, "")
		return &ProvisionResult{Success: false}, nil
	}

	// 3. Allocate a physical slot
	if err := node.AllocateSlot(); err != nil {
		log.Printf("[vps-provisioner] WARNING: failed to allocate slot on %s: %v", node.Code(), err)
		p.enqueuePendingTask(cmd, "")
		return &ProvisionResult{Success: false}, nil
	}
	if err := p.hostRepo.Save(node); err != nil {
		log.Printf("[vps-provisioner] ERROR: failed to save node %s: %v", node.Code(), err)
		return nil, err
	}

	// 4. Enqueue a provisioning task for the agent
	instanceID := p.ids.NewID()
	task := &contracts.Task{
		ID:     p.ids.NewID(),
		NodeID: node.ID(),
		Type:   contracts.TaskProvision,
		Status: contracts.TaskStatusQueued,
		Spec: contracts.ProvisionSpec{
			InstanceID: instanceID,
			Hostname:   cmd.Hostname,
			OS:         cmd.OS,
			CPU:        cmd.CPU,
			MemoryMB:   cmd.MemoryMB,
			DiskGB:     cmd.DiskGB,
			VirtType:   contracts.VirtKVM,
		},
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if err := p.taskRepo.Save(task); err != nil {
		log.Printf("[vps-provisioner] ERROR: failed to enqueue task: %v", err)
		return nil, err
	}

	log.Printf("[vps-provisioner] SUCCESS: task %s on node %s (pool %s) for order %s",
		task.ID, node.Code(), pool.Name(), cmd.OrderID)

	// MVP MOCK: auto-complete the task (remove when real agents are connected)
	p.mockCompleteTask(task)

	return &ProvisionResult{
		InstanceID: instanceID,
		NodeID:     node.ID(),
		TaskID:     task.ID,
		Success:    true,
	}, nil
}

func (p *VPSProvisioner) Release(cmd ProvisionCommand) error {
	log.Printf("[vps-provisioner] releasing resources for order %s", cmd.OrderID)
	// TODO: release slot on node, free IP, etc.
	return nil
}

func (p *VPSProvisioner) buildPool(poolID, regionID string) (*domain.ResourcePool, error) {
	if poolID != "" {
		pool, err := p.poolRepo.GetByID(poolID)
		if err != nil {
			return nil, err
		}
		nodes, err := p.hostRepo.ListEnabledByResourcePoolID(poolID)
		if err != nil {
			return nil, err
		}
		pool.WithNodes(nodes)
		return pool, nil
	}
	if regionID != "" {
		pools, err := p.poolRepo.GetByRegionID(regionID)
		if err != nil {
			return nil, err
		}
		for _, pool := range pools {
			if pool.IsActive() {
				nodes, err := p.hostRepo.ListEnabledByResourcePoolID(pool.ID())
				if err != nil {
					continue
				}
				pool.WithNodes(nodes)
				return pool, nil
			}
		}
	}
	return nil, errors.New("provisioning: no resource pool found")
}

func (p *VPSProvisioner) enqueuePendingTask(cmd ProvisionCommand, nodeID string) {
	instanceID := p.ids.NewID()
	task := &contracts.Task{
		ID:     p.ids.NewID(),
		NodeID: nodeID,
		Type:   contracts.TaskProvision,
		Status: contracts.TaskStatusQueued,
		Spec: contracts.ProvisionSpec{
			InstanceID: instanceID,
			Hostname:   cmd.Hostname,
			OS:         cmd.OS,
			CPU:        cmd.CPU,
			MemoryMB:   cmd.MemoryMB,
			DiskGB:     cmd.DiskGB,
			VirtType:   contracts.VirtKVM,
		},
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if err := p.taskRepo.Save(task); err != nil {
		log.Printf("[vps-provisioner] ERROR: failed to enqueue pending task: %v", err)
	}
}

func (p *VPSProvisioner) mockCompleteTask(task *contracts.Task) {
	task.Status = contracts.TaskStatusCompleted
	task.FinishedAt = time.Now().Format(time.RFC3339)
	if err := p.taskRepo.Save(task); err != nil {
		log.Printf("[vps-provisioner-mock] ERROR: failed to mark task %s completed: %v", task.ID, err)
		return
	}
	log.Printf("[vps-provisioner-mock] task %s auto-completed (mock mode)", task.ID)
}

// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
// Event-driven bridge: subscribes to catalog events and dispatches provisioning
// ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------

// RegisterEventHandlers wires up the dispatcher to listen for catalog events.
func (d *ProvisionDispatcher) RegisterEventHandlers(bus *eventbus.EventBus) {
	bus.Subscribe("product.purchased", d.onProductPurchased)
	bus.Subscribe("product.slot_released", d.onProductSlotReleased)
}

func (d *ProvisionDispatcher) onProductPurchased(evt eventbus.Event) {
	e, ok := evt.(events.ProductPurchasedEvent)
	if !ok {
		return
	}

	cmd := ProvisionCommand{
		ProductID:      e.ProductID,
		ProductSlug:    e.ProductSlug,
		ProductType:    "vps", // default; future: read from event
		ResourcePoolID: e.ResourcePoolID,
		RegionID:       e.RegionID,
		CustomerID:     e.CustomerID,
		OrderID:        e.OrderID,
		Hostname:       e.Hostname,
		OS:             e.OS,
		CPU:            e.CPU,
		MemoryMB:       e.MemoryMB,
		DiskGB:         e.DiskGB,
	}

	if _, err := d.Dispatch(cmd); err != nil {
		log.Printf("[provision-dispatcher] ERROR dispatching for order %s: %v", e.OrderID, err)
	}
}

func (d *ProvisionDispatcher) onProductSlotReleased(evt eventbus.Event) {
	e, ok := evt.(events.ProductSlotReleasedEvent)
	if !ok {
		return
	}
	log.Printf("[provision-dispatcher] slot released: product=%s order=%s", e.ProductID, e.OrderID)
}
