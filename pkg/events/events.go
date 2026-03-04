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
	ProductID   string
	ProductSlug string
	RegionID    string // the region (resource pool) where this product is sold
	CustomerID  string
	OrderID     string
	Hostname    string
	OS          string
	CPU         int
	MemoryMB    int
	DiskGB      int
}

func (ProductPurchasedEvent) EventName() string { return "product.purchased" }

// ProductSlotReleasedEvent is emitted when a product slot is released
// (e.g. cancellation / termination), so the Node domain can free resources.
type ProductSlotReleasedEvent struct {
	ProductID  string
	RegionID   string
	CustomerID string
	OrderID    string
}

func (ProductSlotReleasedEvent) EventName() string { return "product.slot_released" }

// ─── Node Domain Events ──────────────────────────────────────────────────

// ProvisioningCompletedEvent is emitted by the Node domain after an agent
// successfully provisions a VM/container.
type ProvisioningCompletedEvent struct {
	InstanceID string
	NodeID     string
	IPv4       string
	IPv6       string
}

func (ProvisioningCompletedEvent) EventName() string { return "node.provisioning_completed" }

// ProvisioningFailedEvent is emitted when provisioning could not be completed.
type ProvisioningFailedEvent struct {
	InstanceID string
	NodeID     string
	Error      string
}

func (ProvisioningFailedEvent) EventName() string { return "node.provisioning_failed" }
