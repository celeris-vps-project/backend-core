package app

import "context"

// ProductLineDataSource provides region and resource pool information
// needed to build the customer-facing product line view.
// Implemented by the provisioning context via an infra adapter.
type ProductLineDataSource interface {
	ListActiveResourcePools(ctx context.Context) ([]ResourcePoolInfo, error)
	ListActiveRegions(ctx context.Context) ([]RegionInfo, error)
}

// ResourcePoolInfo is a read-model DTO for product line display.
type ResourcePoolInfo struct {
	ID       string
	Name     string
	RegionID string
}

// RegionInfo is a read-model DTO for product line display.
type RegionInfo struct {
	ID       string
	Code     string
	Name     string
	FlagIcon string
}
