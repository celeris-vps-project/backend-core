package domain

// NodeRepository provides persistence for nodes.
type NodeRepository interface {
	GetByID(id string) (*Node, error)
	ListAll() ([]*Node, error)
	ListByLocation(location string) ([]*Node, error)
	Save(node *Node) error
}

// InstanceRepository provides persistence for instances.
type InstanceRepository interface {
	GetByID(id string) (*Instance, error)
	ListByCustomerID(customerID string) ([]*Instance, error)
	ListByNodeID(nodeID string) ([]*Instance, error)
	Save(instance *Instance) error
}
