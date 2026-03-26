package domain

import "backend-core/pkg/contracts"

type RegionRepository interface {
	GetByID(id string) (*Region, error)
	GetByCode(code string) (*Region, error)
	ListAll() ([]*Region, error)
	ListActive() ([]*Region, error)
	Save(region *Region) error
}

type HostNodeRepository interface {
	GetByID(id string) (*HostNode, error)
	GetByCode(code string) (*HostNode, error)
	GetByNodeToken(token string) (*HostNode, error) // lookup by permanent node credential
	ListAll() ([]*HostNode, error)
	ListByLocation(location string) ([]*HostNode, error)
	ListByRegionID(regionID string) ([]*HostNode, error)
	ListEnabledByRegionID(regionID string) ([]*HostNode, error)
	ListByResourcePoolID(poolID string) ([]*HostNode, error)
	ListEnabledByResourcePoolID(poolID string) ([]*HostNode, error)
	Save(node *HostNode) error

	// AllocateSlotAtomic atomically increments used_slots by 1 using a
	// database-level conditional UPDATE. This prevents the read-modify-write
	// race condition when multiple goroutines try to allocate on the same node.
	//
	// Returns an error if the node is disabled, not found, or has no capacity.
	AllocateSlotAtomic(nodeID string) error

	// ReleaseSlotAtomic atomically decrements used_slots by 1.
	ReleaseSlotAtomic(nodeID string) error
}

// BootstrapTokenRepository persists one-time bootstrap tokens for agent registration.
type BootstrapTokenRepository interface {
	GetByToken(token string) (*BootstrapToken, error)
	GetByID(id string) (*BootstrapToken, error)
	ListAll() ([]*BootstrapToken, error)
	Save(bt *BootstrapToken) error
	Delete(id string) error
}

type ResourcePoolRepository interface {
	GetByID(id string) (*ResourcePool, error)
	GetByRegionID(regionID string) ([]*ResourcePool, error)
	ListAll() ([]*ResourcePool, error)
	ListActive() ([]*ResourcePool, error)
	Save(pool *ResourcePool) error
	Delete(id string) error
}

type IPAddressRepository interface {
	GetByID(id string) (*IPAddress, error)
	ListByNodeID(nodeID string) ([]*IPAddress, error)
	FindAvailable(nodeID string, version int) (*IPAddress, error)
	Save(ip *IPAddress) error

	// NAT port allocation support

	// ListNATPortsByNodeID returns all allocated NAT ports on a node (both assigned and released).
	ListNATPortsByNodeID(nodeID string) ([]int, error)
	// FindAvailableNAT returns an available (unassigned) NAT port allocation on the node, if any.
	FindAvailableNAT(nodeID string) (*IPAddress, error)
}

// TaskRepository persists provisioning tasks for agents to poll.
type TaskRepository interface {
	GetByID(id string) (*contracts.Task, error)
	ListPendingByNodeID(nodeID string) ([]contracts.Task, error)
	Save(task *contracts.Task) error
}
