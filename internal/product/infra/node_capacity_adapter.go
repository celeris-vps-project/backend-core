package infra

import (
	nodeDomain "backend-core/internal/node/domain"
)

// NodeCapacityAdapter implements product/domain.PhysicalCapacityChecker by
// delegating to the Node bounded context's HostNodeRepository.
// This is the anti-corruption layer that lets the Product app service
// query physical capacity without importing the Node domain directly.
type NodeCapacityAdapter struct {
	hostRepo nodeDomain.HostNodeRepository
}

func NewNodeCapacityAdapter(hostRepo nodeDomain.HostNodeRepository) *NodeCapacityAdapter {
	return &NodeCapacityAdapter{hostRepo: hostRepo}
}

// AvailablePhysicalSlots returns the sum of available physical slots across
// all enabled nodes in the given region.
func (a *NodeCapacityAdapter) AvailablePhysicalSlots(regionID string) (int, error) {
	nodes, err := a.hostRepo.ListEnabledByRegionID(regionID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, n := range nodes {
		total += n.AvailableSlots()
	}
	return total, nil
}
