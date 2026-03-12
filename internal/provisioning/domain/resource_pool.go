package domain

import "errors"

// ResourcePool - a pure physical resource grouping entity.
//
// A pool aggregates HostNodes deployed in one geographic region, providing
// load-balanced capacity for provisioning. It contains NO sales/catalog
// attributes (description, sortOrder) - those belong to catalog.Category.
//
// One ResourcePool <- many HostNodes.
// One ResourcePool <- one catalog.Category (via Category.resourcePoolID).

const (
	PoolStatusActive   = "active"
	PoolStatusInactive = "inactive"
)

// ResourcePool represents a named group of nodes that share capacity.
// It is a pure infrastructure entity - no customer-facing attributes.
type ResourcePool struct {
	id       string
	name     string // internal admin name, e.g. "fra-cdn77-pool-01"
	regionID string // FK to Region - the geographic region this pool covers
	status   string

	// nodes are loaded lazily when building the capacity view.
	// They are NOT part of the persisted fields - they come from HostNode.resourcePoolID.
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

func ReconstituteResourcePool(id, name, regionID, status string) *ResourcePool {
	return &ResourcePool{
		id:       id,
		name:     name,
		regionID: regionID,
		status:   status,
	}
}

func (p *ResourcePool) ID() string         { return p.id }
func (p *ResourcePool) Name() string       { return p.name }
func (p *ResourcePool) RegionID() string   { return p.regionID }
func (p *ResourcePool) Status() string     { return p.status }
func (p *ResourcePool) Nodes() []*HostNode { return p.nodes }

func (p *ResourcePool) SetName(name string)   { p.name = name }
func (p *ResourcePool) SetRegionID(id string) { p.regionID = id }
func (p *ResourcePool) Activate()             { p.status = PoolStatusActive }
func (p *ResourcePool) Deactivate()           { p.status = PoolStatusInactive }
func (p *ResourcePool) IsActive() bool        { return p.status == PoolStatusActive }

// WithNodes attaches nodes to the pool for capacity computation.
// The nodes are NOT persisted here - they reference the pool via HostNode.resourcePoolID.
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
