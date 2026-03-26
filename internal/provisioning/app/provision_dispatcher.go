package app

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"backend-core/pkg/delayed"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"context"
	"encoding/json"
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
	VirtType       string // "kvm" or "lxc", defaults to "kvm"
	StoragePool    string // e.g. "default", "zfs-pool"
	NetworkName    string // e.g. "incusbr0", "br0"
	NetworkMode    string // "dedicated" or "nat"; empty = dedicated
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
// It selects a node from the resource pool, allocates a slot, enqueues
// a provisioning task for the agent, and schedules an async boot
// confirmation check via the delayed Publisher.
type VPSProvisioner struct {
	hostRepo       domain.HostNodeRepository
	poolRepo       domain.ResourcePoolRepository
	taskRepo       domain.TaskRepository
	ipRepo         domain.IPAddressRepository // for NAT port allocation
	stateCache     domain.NodeStateCache      // for resolving host IP in NAT mode
	ids            IDGenerator
	delayPublisher delayed.Publisher // async boot confirmation queue (nil = skip)
	bootCheckDelay time.Duration    // how long to wait before checking boot status
	mockMode       bool             // when true, auto-complete tasks (dev/test without real agents)
}

// NewVPSProvisioner creates a provisioner for VPS-type products.
// The delayPublisher is optional — if nil, boot confirmation scheduling
// is skipped (useful for tests or when running without a queue backend).
func NewVPSProvisioner(
	hostRepo domain.HostNodeRepository,
	poolRepo domain.ResourcePoolRepository,
	taskRepo domain.TaskRepository,
	ids IDGenerator,
	opts ...VPSProvisionerOption,
) *VPSProvisioner {
	p := &VPSProvisioner{
		hostRepo:       hostRepo,
		poolRepo:       poolRepo,
		taskRepo:       taskRepo,
		ids:            ids,
		bootCheckDelay: 30 * time.Second, // default: check boot after 30s
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// VPSProvisionerOption configures a VPSProvisioner.
type VPSProvisionerOption func(*VPSProvisioner)

// WithDelayedPublisher sets the delayed publisher for async boot confirmation.
func WithDelayedPublisher(pub delayed.Publisher) VPSProvisionerOption {
	return func(p *VPSProvisioner) {
		p.delayPublisher = pub
	}
}

// WithBootCheckDelay overrides the default boot confirmation delay (30s).
func WithBootCheckDelay(d time.Duration) VPSProvisionerOption {
	return func(p *VPSProvisioner) {
		p.bootCheckDelay = d
	}
}

// WithMockMode enables mock mode: tasks are auto-completed without a real agent.
// Use for development/testing when no agent is connected. Default is false
// (production mode — tasks stay queued until a real agent picks them up).
func WithMockMode(mock bool) VPSProvisionerOption {
	return func(p *VPSProvisioner) {
		p.mockMode = mock
	}
}

// WithIPRepo sets the IP address repository for NAT port allocation.
func WithIPRepo(repo domain.IPAddressRepository) VPSProvisionerOption {
	return func(p *VPSProvisioner) {
		p.ipRepo = repo
	}
}

// WithStateCache sets the node state cache for resolving host IP in NAT mode.
func WithStateCache(cache domain.NodeStateCache) VPSProvisionerOption {
	return func(p *VPSProvisioner) {
		p.stateCache = cache
	}
}

// resolveVirtType converts a string virt type to the contracts.VirtType enum.
// Defaults to KVM if empty or unrecognised.
func resolveVirtType(vt string) contracts.VirtType {
	switch vt {
	case "lxc", "container":
		return contracts.VirtLXC
	default:
		return contracts.VirtKVM
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

	// 4. Build the provision spec
	virtType := resolveVirtType(cmd.VirtType)
	instanceID := p.ids.NewID()
	spec := contracts.ProvisionSpec{
		InstanceID:  instanceID,
		Hostname:    cmd.Hostname,
		OS:          cmd.OS,
		CPU:         cmd.CPU,
		MemoryMB:    cmd.MemoryMB,
		DiskGB:      cmd.DiskGB,
		VirtType:    virtType,
		StoragePool: cmd.StoragePool,
		NetworkName: cmd.NetworkName,
	}

	// 4a. Network resource allocation (dedicated IP or NAT port)
	if cmd.NetworkMode == "nat" {
		natPort, err := p.allocateNATPort(node, instanceID)
		if err != nil {
			log.Printf("[vps-provisioner] WARNING: NAT port allocation failed on %s: %v", node.Code(), err)
			// Non-fatal: continue provisioning, agent will need manual NAT setup
		} else {
			spec.NetworkMode = contracts.NetworkModeNAT
			spec.NATPort = natPort
			log.Printf("[vps-provisioner] NAT port %d allocated on node %s for instance %s",
				natPort, node.Code(), instanceID)
		}
	}

	// 5. Enqueue a provisioning task for the agent
	task := &contracts.Task{
		ID:        p.ids.NewID(),
		NodeID:    node.ID(),
		Type:      contracts.TaskProvision,
		Status:    contracts.TaskStatusQueued,
		Spec:      spec,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if err := p.taskRepo.Save(task); err != nil {
		log.Printf("[vps-provisioner] ERROR: failed to enqueue task: %v", err)
		return nil, err
	}

	log.Printf("[vps-provisioner] SUCCESS: task %s on node %s (pool %s) for order %s virt=%s network=%s",
		task.ID, node.Code(), pool.Name(), cmd.OrderID, virtType, cmd.NetworkMode)

	// 5. Schedule async boot confirmation check via delayed queue.
	// After bootCheckDelay, the BootConfirmationWorker verifies whether
	// the agent has completed the provisioning task. Works with both
	// InMemoryPublisher (dev) and AsynqPublisher (production).
	p.scheduleBootConfirmation(instanceID, task.ID, node.ID())

	// Mock mode: auto-complete the task for dev/test without real agents.
	// In production (mockMode=false), tasks stay queued until a real agent
	// picks them up via heartbeat polling.
	if p.mockMode {
		p.mockCompleteTask(task)
	}

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
			InstanceID:  instanceID,
			Hostname:    cmd.Hostname,
			OS:          cmd.OS,
			CPU:         cmd.CPU,
			MemoryMB:    cmd.MemoryMB,
			DiskGB:      cmd.DiskGB,
			VirtType:    resolveVirtType(cmd.VirtType),
			StoragePool: cmd.StoragePool,
			NetworkName: cmd.NetworkName,
		},
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	if err := p.taskRepo.Save(task); err != nil {
		log.Printf("[vps-provisioner] ERROR: failed to enqueue pending task: %v", err)
	}
}

// allocateNATPort finds a free port on the node's NAT port range, creates an
// IPAddress record (mode=nat) in the ip_pool, assigns it to the instance,
// and returns the allocated port number.
func (p *VPSProvisioner) allocateNATPort(node *domain.HostNode, instanceID string) (int, error) {
	if p.ipRepo == nil {
		return 0, errors.New("provisioning: ipRepo not configured for NAT allocation")
	}
	if !node.HasNATPortPool() {
		return 0, errors.New("provisioning: node " + node.Code() + " has no NAT port pool configured")
	}

	// 1. Try to reuse a previously released (available) NAT port allocation
	existing, err := p.ipRepo.FindAvailableNAT(node.ID())
	if err == nil && existing != nil {
		if err := existing.Assign(instanceID); err != nil {
			return 0, err
		}
		if err := p.ipRepo.Save(existing); err != nil {
			return 0, err
		}
		return existing.Port(), nil
	}

	// 2. No reusable allocation — find a free port from the node's range
	usedPorts, err := p.ipRepo.ListNATPortsByNodeID(node.ID())
	if err != nil {
		return 0, err
	}
	usedSet := make(map[int]struct{}, len(usedPorts))
	for _, port := range usedPorts {
		usedSet[port] = struct{}{}
	}
	freePort, err := node.FindFreeNATPort(usedSet)
	if err != nil {
		return 0, err
	}

	// 3. Create a new IPAddress record (mode=nat) and assign it
	alloc, err := domain.NewNATPortAllocation(p.ids.NewID(), node.ID(), freePort)
	if err != nil {
		return 0, err
	}
	if err := alloc.Assign(instanceID); err != nil {
		return 0, err
	}
	if err := p.ipRepo.Save(alloc); err != nil {
		return 0, err
	}
	return freePort, nil
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

// scheduleBootConfirmation publishes a delayed "provision.confirm_boot" event
// to the configured Publisher. After bootCheckDelay, the BootConfirmationWorker
// (registered in the delayed Router) will check whether the provisioning task
// has completed successfully.
//
// If no Publisher is configured (nil), this is a no-op.
func (p *VPSProvisioner) scheduleBootConfirmation(instanceID, taskID, nodeID string) {
	if p.delayPublisher == nil {
		return
	}

	payload, err := json.Marshal(map[string]string{
		"instance_id": instanceID,
		"task_id":     taskID,
		"node_id":     nodeID,
	})
	if err != nil {
		log.Printf("[vps-provisioner] ERROR: failed to marshal boot confirm payload: %v", err)
		return
	}

	if err := p.delayPublisher.PublishDelayed(
		context.Background(),
		"provision.confirm_boot",
		payload,
		p.bootCheckDelay,
	); err != nil {
		log.Printf("[vps-provisioner] WARNING: failed to schedule boot confirmation for instance=%s: %v",
			instanceID, err)
		// Non-fatal — provisioning still proceeds, just without async confirmation.
	} else {
		log.Printf("[vps-provisioner] scheduled boot confirmation: instance=%s delay=%v",
			instanceID, p.bootCheckDelay)
	}
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
		NetworkMode:    e.NetworkMode,
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
