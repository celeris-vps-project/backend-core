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
}

// TaskRepository persists provisioning tasks for agents to poll.
type TaskRepository interface {
	GetByID(id string) (*contracts.Task, error)
	ListPendingByNodeID(nodeID string) ([]contracts.Task, error)
	Save(task *contracts.Task) error
}
