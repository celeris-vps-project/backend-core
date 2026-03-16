package domain

type ProductRepository interface {
	GetByID(id string) (*Product, error)
	GetBySlug(slug string) (*Product, error)
	ListAll() ([]*Product, error)
	ListEnabled() ([]*Product, error)
	ListByRegionID(regionID string) ([]*Product, error)
	Save(product *Product) error

	// ConsumeSlotAtomic atomically increments sold_slots by 1 using a database-level
	// conditional UPDATE. This prevents the read-modify-write race condition that
	// exists when using GetByID → ConsumeSlot → Save under concurrent requests.
	//
	// The UPDATE only succeeds if:
	//   - The product is enabled
	//   - total_slots == -1 (unlimited) OR sold_slots < total_slots
	//
	// Returns ErrNoAvailableSlots if the atomic update matched zero rows.
	ConsumeSlotAtomic(productID string) error

	// ReleaseSlotAtomic atomically decrements sold_slots by 1.
	// Returns an error if sold_slots is already 0.
	ReleaseSlotAtomic(productID string) error
}

// PhysicalCapacityChecker is a port (interface) that the Product application
// service uses to query the physical capacity of the resource pool (Node domain).
// This is the anti-corruption layer: the Product domain never imports node packages.
type PhysicalCapacityChecker interface {
	// AvailablePhysicalSlots returns the number of unused physical slots
	// in the resource pool (region) identified by regionID.
	AvailablePhysicalSlots(regionID string) (int, error)
}
