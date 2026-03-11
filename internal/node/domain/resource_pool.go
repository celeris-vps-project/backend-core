package domain

import "errors"

// ──────────────────────────────────────────────────────────────────────────
// ResourcePool — a first-class persisted entity that groups HostNodes.
//
// A pool is region-based: it aggregates nodes deployed in one geographic
// region, providing load-balanced capacity for product provisioning.
//
// One ResourcePool → many HostNodes.
// One ResourcePool → many Products (via Product.resourcePoolID).
// ──────────────────────────────────────────────────────────────────────────

const (
	PoolStatusActive   = "active"
	PoolStatusInactive = "inactive"
)

// ResourcePool represents a named group of nodes that share capacity.
// It IS a persisted entity with its own identity.
//
// A ResourcePool also serves as a customer-facing "product line" — e.g.
// "Frankfurt – CDN77 Optimized" or "New York – Standard". The description
// and sortOrder fields control how it appears in the public catalog.
type ResourcePool struct {
	id          string
	name        string
	regionID    string // FK to Region — the geographic region this pool covers
	status      string
	description string // customer-visible description (e.g. "Premium CDN77+GSL transit")
	sortOrder   int    // display order in the public catalog

	// nodes are loaded lazily when building the capacity view.
	// They are NOT part of the persisted fields — they come from HostNode.resourcePoolID.
	nodes []*HostNode
}

func NewResourcePool(id, name, regionID string) (*ResourcePool, error) {
	if id == "" {
		return nil, errors.New("domain_error: resource pool id is required")
	}
	if name == "" {
		return nil, errors.New("domain_error: resource pool name is required")
	}
	if regionID == "" {
		return nil, errors.New("domain_error: region id is required")
	}
	return &ResourcePool{
		id:       id,
		name:     name,
		regionID: regionID,
		status:   PoolStatusActive,
	}, nil
}

func ReconstituteResourcePool(id, name, regionID, status, description string, sortOrder int) *ResourcePool {
	return &ResourcePool{
		id:          id,
		name:        name,
		regionID:    regionID,
		status:      status,
		description: description,
		sortOrder:   sortOrder,
	}
}

func (p *ResourcePool) ID() string          { return p.id }
func (p *ResourcePool) Name() string        { return p.name }
func (p *ResourcePool) RegionID() string    { return p.regionID }
func (p *ResourcePool) Status() string      { return p.status }
func (p *ResourcePool) Description() string { return p.description }
func (p *ResourcePool) SortOrder() int      { return p.sortOrder }
func (p *ResourcePool) Nodes() []*HostNode  { return p.nodes }

func (p *ResourcePool) SetName(name string)          { p.name = name }
func (p *ResourcePool) SetRegionID(id string)        { p.regionID = id }
func (p *ResourcePool) SetDescription(desc string)   { p.description = desc }
func (p *ResourcePool) SetSortOrder(order int)       { p.sortOrder = order }
func (p *ResourcePool) Activate()                    { p.status = PoolStatusActive }
func (p *ResourcePool) Deactivate()                  { p.status = PoolStatusInactive }
func (p *ResourcePool) IsActive() bool               { return p.status == PoolStatusActive }

// WithNodes attaches nodes to the pool for capacity computation.
// The nodes are NOT persisted here — they reference the pool via HostNode.resourcePoolID.
func (p *ResourcePool) WithNodes(nodes []*HostNode) {
	p.nodes = nodes
}

// TotalPhysicalSlots returns the sum of total_slots across all nodes in the pool.
func (p *ResourcePool) TotalPhysicalSlots() int {
	total := 0
	for _, n := range p.nodes {
		total += n.TotalSlots()
	}
	return total
}

// UsedPhysicalSlots returns the sum of used_slots across all nodes in the pool.
func (p *ResourcePool) UsedPhysicalSlots() int {
	used := 0
	for _, n := range p.nodes {
		used += n.UsedSlots()
	}
	return used
}

// AvailablePhysicalSlots returns total - used across the pool.
func (p *ResourcePool) AvailablePhysicalSlots() int {
	avail := p.TotalPhysicalSlots() - p.UsedPhysicalSlots()
	if avail < 0 {
		return 0
	}
	return avail
}

// SelectNode picks the best available node for provisioning using a
// least-loaded strategy: choose the enabled node with the most available
// slots (simple load balancing).
func (p *ResourcePool) SelectNode() (*HostNode, error) {
	var best *HostNode
	bestAvail := 0
	for _, n := range p.nodes {
		if !n.HasCapacity() {
			continue
		}
		avail := n.AvailableSlots()
		if best == nil || avail > bestAvail {
			best = n
			bestAvail = avail
		}
	}
	if best == nil {
		return nil, errors.New("domain_error: no available nodes in resource pool " + p.name)
	}
	return best, nil
}

// HasCapacity returns true if at least one node in the pool has a free slot.
func (p *ResourcePool) HasCapacity() bool {
	for _, n := range p.nodes {
		if n.HasCapacity() {
			return true
		}
	}
	return false
}
