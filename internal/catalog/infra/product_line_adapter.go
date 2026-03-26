package infra

import (
	catalogApp "backend-core/internal/catalog/app"
	provApp "backend-core/internal/provisioning/app"
	"context"
)

// ProvisioningProductLineAdapter implements catalogApp.ProductLineDataSource
// by delegating to the provisioning app service.
type ProvisioningProductLineAdapter struct {
	provSvc *provApp.ProvisioningAppService
}

func NewProvisioningProductLineAdapter(provSvc *provApp.ProvisioningAppService) *ProvisioningProductLineAdapter {
	return &ProvisioningProductLineAdapter{provSvc: provSvc}
}

func (a *ProvisioningProductLineAdapter) ListActiveResourcePools(ctx context.Context) ([]catalogApp.ResourcePoolInfo, error) {
	pools, err := a.provSvc.ListActiveResourcePools()
	if err != nil {
		return nil, err
	}
	result := make([]catalogApp.ResourcePoolInfo, len(pools))
	for i, p := range pools {
		result[i] = catalogApp.ResourcePoolInfo{
			ID:       p.ID(),
			Name:     p.Name(),
			RegionID: p.RegionID(),
		}
	}
	return result, nil
}

func (a *ProvisioningProductLineAdapter) ListActiveRegions(ctx context.Context) ([]catalogApp.RegionInfo, error) {
	regions, err := a.provSvc.ListActiveRegions()
	if err != nil {
		return nil, err
	}
	result := make([]catalogApp.RegionInfo, len(regions))
	for i, r := range regions {
		result[i] = catalogApp.RegionInfo{
			ID:       r.ID(),
			Code:     r.Code(),
			Name:     r.Name(),
			FlagIcon: r.FlagIcon(),
		}
	}
	return result, nil
}
