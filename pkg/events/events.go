// Package events defines the domain events shared across bounded contexts.
//
// These are integration events — they flow between Product, Node, and Instance
// contexts via the EventBus. Each bounded context owns its own events but
// other contexts may subscribe to them.
package events

// ─── Product Domain Events ────────────────────────────────────────────────

// ProductPurchasedEvent is emitted by the Product domain when a user
// successfully purchases a product (commercial slot consumed).
// The Node domain listens to this event to trigger physical provisioning.
type ProductPurchasedEvent struct {
	ProductID      string
	ProductSlug    string
	RegionID       string // the region where this product is sold (legacy)
	ResourcePoolID string // the resource pool this product belongs to
	CustomerID     string
	OrderID        string
	InstanceID     string // optional: the pre-created instance record to fulfill
	Hostname       string
	OS             string
	CPU            int
	MemoryMB       int
	DiskGB         int
	NetworkMode    string // "dedicated" or "nat"; empty = dedicated
}

func (ProductPurchasedEvent) EventName() string { return "product.purchased" }

// ProductSlotReleasedEvent is emitted when a product slot is released
// (e.g. cancellation / termination), so the Node domain can free resources.
type ProductSlotReleasedEvent struct {
	ProductID   string
	RegionID    string
	CustomerID  string
	OrderID     string
	InstanceID  string
	NodeID      string
	NetworkMode string
}

func (ProductSlotReleasedEvent) EventName() string { return "product.slot_released" }

// ─── Node Domain Events ──────────────────────────────────────────────────

// ProvisioningCompletedEvent is emitted by the Node domain after an agent
// successfully provisions a VM/container. The Instance domain subscribes
// to this event to update instance status, assign IP, and record NAT port.
type ProvisioningCompletedEvent struct {
	InstanceID  string
	NodeID      string
	TaskID      string
	IPv4        string
	IPv6        string
	VMState     string // "running", "boot_timeout", etc.
	NetworkMode string // "dedicated" or "nat"
	NATPort     int    // NAT mode: the high port on the host mapped to VM SSH
	HostIP      string // NAT mode: the host's public IP for external access
}

func (ProvisioningCompletedEvent) EventName() string { return "node.provisioning_completed" }

// ProvisioningFailedEvent is emitted when provisioning could not be completed.
type ProvisioningFailedEvent struct {
	InstanceID string
	NodeID     string
	TaskID     string
	Error      string
}

func (ProvisioningFailedEvent) EventName() string { return "node.provisioning_failed" }

type InstanceTaskCompletedEvent struct {
	InstanceID string
	NodeID     string
	TaskID     string
	TaskType   string
	IPv4       string
	IPv6       string
	VMState    string
}

func (InstanceTaskCompletedEvent) EventName() string { return "node.instance_task_completed" }

type InstanceTaskFailedEvent struct {
	InstanceID string
	NodeID     string
	TaskID     string
	TaskType   string
	Error      string
}

func (InstanceTaskFailedEvent) EventName() string { return "node.instance_task_failed" }

// NodeStateUpdatedEvent is emitted whenever a node's runtime state changes
// (agent registration or heartbeat). The WebSocket hub listens to this event
// to push real-time updates to connected admin clients.
type NodeStateUpdatedEvent struct {
	NodeID    string  `json:"node_id"`
	Status    string  `json:"status"`
	IP        string  `json:"ip,omitempty"`
	AgentVer  string  `json:"agent_ver,omitempty"`
	CPUUsage  float64 `json:"cpu_usage"`
	MemUsage  float64 `json:"mem_usage"`
	DiskUsage float64 `json:"disk_usage"`
	VMCount   int     `json:"vm_count"`
	LastSeen  string  `json:"last_seen_at"`
}

func (NodeStateUpdatedEvent) EventName() string { return "node.state_updated" }

// InstanceStateUpdatedEvent is emitted whenever the customer-visible runtime
// state of an instance changes. The customer-facing WebSocket hub listens to
// this event and forwards matching updates to the owning user.
type InstanceStateUpdatedEvent struct {
	InstanceID   string  `json:"id"`
	CustomerID   string  `json:"-"`
	OrderID      string  `json:"order_id"`
	NodeID       string  `json:"node_id"`
	Hostname     string  `json:"hostname"`
	Plan         string  `json:"plan"`
	OS           string  `json:"os"`
	CPU          int     `json:"cpu"`
	MemoryMB     int     `json:"memory_mb"`
	DiskGB       int     `json:"disk_gb"`
	IPv4         string  `json:"ipv4,omitempty"`
	IPv6         string  `json:"ipv6,omitempty"`
	Status       string  `json:"status"`
	NetworkMode  string  `json:"network_mode,omitempty"`
	NATPort      int     `json:"nat_port,omitempty"`
	CreatedAt    string  `json:"created_at"`
	StartedAt    *string `json:"started_at,omitempty"`
	StoppedAt    *string `json:"stopped_at,omitempty"`
	SuspendedAt  *string `json:"suspended_at,omitempty"`
	TerminatedAt *string `json:"terminated_at,omitempty"`
}

func (InstanceStateUpdatedEvent) EventName() string { return "instance.state_updated" }
