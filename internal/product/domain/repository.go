package domain

type ProductRepository interface {
	GetByID(id string) (*Product, error)
	GetBySlug(slug string) (*Product, error)
	ListAll() ([]*Product, error)
	ListEnabled() ([]*Product, error)
	ListByRegionID(regionID string) ([]*Product, error)
	Save(product *Product) error
}

// PhysicalCapacityChecker is a port (interface) that the Product application
// service uses to query the physical capacity of the resource pool (Node domain).
// This is the anti-corruption layer: the Product domain never imports node packages.
type PhysicalCapacityChecker interface {
	// AvailablePhysicalSlots returns the number of unused physical slots
	// in the resource pool (region) identified by regionID.
	AvailablePhysicalSlots(regionID string) (int, error)
}
