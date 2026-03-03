package domain

import "backend-core/pkg/contracts"

type HostNodeRepository interface {
	GetByID(id string) (*HostNode, error)
	GetByCode(code string) (*HostNode, error)
	ListAll() ([]*HostNode, error)
	ListByLocation(location string) ([]*HostNode, error)
	Save(node *HostNode) error
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
