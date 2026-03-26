package infra

import (
	"context"
	nodeDomain "backend-core/internal/provisioning/domain"
)

// NodeCapacityAdapter implements product/domain.PhysicalCapacityChecker by
// delegating to the Node bounded context's HostNodeRepository.
type NodeCapacityAdapter struct {
	hostRepo nodeDomain.HostNodeRepository
}

func NewNodeCapacityAdapter(hostRepo nodeDomain.HostNodeRepository) *NodeCapacityAdapter {
	return &NodeCapacityAdapter{hostRepo: hostRepo}
}

func (a *NodeCapacityAdapter) AvailablePhysicalSlots(ctx context.Context, regionID string) (int, error) {
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
