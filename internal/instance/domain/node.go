package domain

// NodeAllocator is a narrow interface that the instance bounded context uses
// to check capacity and allocate/release slots on a physical node.
// It is satisfied by node/domain.HostNode — keeping the two bounded contexts
// decoupled without duplicating the node concept.
type NodeAllocator interface {
	ID() string
	Code() string
	Location() string
	Name() string
	TotalSlots() int
	UsedSlots() int
	AvailableSlots() int
	HasCapacity() bool
	Enabled() bool
	AllocateSlot() error
	ReleaseSlot() error
}
