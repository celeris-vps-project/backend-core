package domain

// NodeAllocatorRepository provides access to nodes for capacity management.
// The underlying implementation delegates to the node bounded context's HostNodeRepository.
type NodeAllocatorRepository interface {
	GetByID(id string) (NodeAllocator, error)
	ListAll() ([]NodeAllocator, error)
	ListByLocation(location string) ([]NodeAllocator, error)
	AllocateSlotAtomic(nodeID string) error
	ReleaseSlotAtomic(nodeID string) error
	Save(node NodeAllocator) error
}

// InstanceRepository provides persistence for instances.
type InstanceRepository interface {
	GetByID(id string) (*Instance, error)
	GetByOrderID(orderID string) (*Instance, error)
	ListAll() ([]*Instance, error)
	ListByCustomerID(customerID string) ([]*Instance, error)
	ListByNodeID(nodeID string) ([]*Instance, error)
	Save(instance *Instance) error
}
