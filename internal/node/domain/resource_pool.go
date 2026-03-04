package domain

import "errors"

// ──────────────────────────────────────────────────────────────────────────
// Resource Pooling Strategy: REGION-BASED
//
// We group nodes into resource pools by Region rather than by hardware tier
// because:
//
//  1. Customers select VPS by geographic location — this is the primary
//     dimension in every hosting control panel (e.g. "Frankfurt", "US-West").
//
//  2. Regions are already a first-class domain concept (the Region entity
//     already exists, nodes already carry a regionID).
//
//  3. Latency / legal jurisdiction are location-driven concerns; mixing
//     regions in a single pool would violate user expectations.
//
//  4. Hardware tiers can be expressed as Product-level metadata (cpu, mem,
//     disk) without needing a separate pooling dimension. Within a region
//     pool, the scheduler can match a product's resource requirements to
//     a node with sufficient capacity.
//
//  5. This keeps the model simple and avoids a combinatorial explosion
//     of Region × Tier pools.
//
// A ResourcePool is a read-model / domain service concept that aggregates
// capacity across all enabled HostNodes in a given region.
// ──────────────────────────────────────────────────────────────────────────

// ResourcePool represents the aggregated physical capacity of all nodes
// in a single region. It is NOT a persisted entity — it is a computed
// view derived from the Region + its HostNodes.
type ResourcePool struct {
	regionID   string
	regionCode string
	regionName string
	nodes      []*HostNode
}

func NewResourcePool(regionID, regionCode, regionName string, nodes []*HostNode) *ResourcePool {
	return &ResourcePool{
		regionID:   regionID,
		regionCode: regionCode,
		regionName: regionName,
		nodes:      nodes,
	}
}

func (p *ResourcePool) RegionID() string   { return p.regionID }
func (p *ResourcePool) RegionCode() string { return p.regionCode }
func (p *ResourcePool) RegionName() string { return p.regionName }
func (p *ResourcePool) Nodes() []*HostNode { return p.nodes }

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
		return nil, errors.New("domain_error: no available nodes in resource pool " + p.regionCode)
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
