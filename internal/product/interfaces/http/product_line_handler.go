package http

import (
	nodeApp "backend-core/internal/node/app"
	"backend-core/internal/product/app"
	"context"
	"sort"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ──────────────────────────────────────────────────────────────────────────
// ProductLineHandler — public customer-facing endpoint for browsing
// available product lines (resource pools enriched with region info).
//
// A "product line" is the customer-facing projection of a ResourcePool,
// e.g. "Frankfurt – CDN77 Optimized" or "New York – Standard".
// Customers browse product lines first, then pick a plan within one.
//
// This handler sits in the interfaces layer and is allowed to orchestrate
// across bounded contexts (product + node) for read-model queries.
// ──────────────────────────────────────────────────────────────────────────

// ProductLineResponse is the public DTO returned by GET /product-lines.
type ProductLineResponse struct {
	ID           string `json:"id"`            // ResourcePool ID
	Name         string `json:"name"`          // e.g. "Frankfurt – CDN77 Optimized"
	Description  string `json:"description"`   // e.g. "Premium CDN77+GSL transit"
	RegionCode   string `json:"region_code"`   // e.g. "DE-fra"
	RegionName   string `json:"region_name"`   // e.g. "Frankfurt, Germany"
	FlagIcon     string `json:"flag_icon"`     // e.g. "🇩🇪"
	SortOrder    int    `json:"sort_order"`
	ProductCount int    `json:"product_count"` // number of enabled products in this line
	MinPrice     int64  `json:"min_price"`     // lowest price_amount among products
	MinCurrency  string `json:"min_currency"`  // currency of the cheapest product
	MinCycle     string `json:"min_cycle"`     // billing_cycle of the cheapest product
}

// ProductLineHandler serves customer-facing product line browsing.
type ProductLineHandler struct {
	prodSvc *app.ProductAppService
	nodeSvc *nodeApp.NodeAppService
}

// NewProductLineHandler creates a handler that combines product and node data.
func NewProductLineHandler(prodSvc *app.ProductAppService, nodeSvc *nodeApp.NodeAppService) *ProductLineHandler {
	return &ProductLineHandler{prodSvc: prodSvc, nodeSvc: nodeSvc}
}

// List returns all active product lines with enriched region info and
// product statistics (count, min price). This replaces the old GET /groups
// endpoint for customer-facing catalog browsing.
//
// GET /api/v1/product-lines
func (h *ProductLineHandler) List(ctx context.Context, c *hz_app.RequestContext) {
	// 1. Get all active resource pools
	pools, err := h.nodeSvc.ListActiveResourcePools()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	// 2. Build a region lookup map
	regions, err := h.nodeSvc.ListActiveRegions()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}
	regionMap := make(map[string]struct {
		Code     string
		Name     string
		FlagIcon string
	})
	for _, r := range regions {
		regionMap[r.ID()] = struct {
			Code     string
			Name     string
			FlagIcon string
		}{Code: r.Code(), Name: r.Name(), FlagIcon: r.FlagIcon()}
	}

	// 3. Get all enabled products to compute per-pool stats
	products, err := h.prodSvc.ListEnabled()
	if err != nil {
		c.JSON(consts.StatusInternalServerError, utils.H{"error": err.Error()})
		return
	}

	// 4. Group products by resource_pool_id
	type poolStats struct {
		count       int
		minPrice    int64
		minCurrency string
		minCycle    string
	}
	statsMap := make(map[string]*poolStats)
	for _, p := range products {
		poolID := p.ResourcePoolID()
		if poolID == "" {
			continue
		}
		st, ok := statsMap[poolID]
		if !ok {
			st = &poolStats{minPrice: p.PriceAmount(), minCurrency: p.Currency(), minCycle: string(p.BillingCycle())}
			statsMap[poolID] = st
		}
		st.count++
		if p.PriceAmount() < st.minPrice {
			st.minPrice = p.PriceAmount()
			st.minCurrency = p.Currency()
			st.minCycle = string(p.BillingCycle())
		}
	}

	// 5. Build response — only include pools that have at least one product
	var result []ProductLineResponse
	for _, pool := range pools {
		st := statsMap[pool.ID()]
		if st == nil || st.count == 0 {
			continue // skip empty product lines
		}

		region := regionMap[pool.RegionID()]
		result = append(result, ProductLineResponse{
			ID:           pool.ID(),
			Name:         pool.Name(),
			Description:  pool.Description(),
			RegionCode:   region.Code,
			RegionName:   region.Name,
			FlagIcon:     region.FlagIcon,
			SortOrder:    pool.SortOrder(),
			ProductCount: st.count,
			MinPrice:     st.minPrice,
			MinCurrency:  st.minCurrency,
			MinCycle:     st.minCycle,
		})
	}

	// 6. Sort by sort_order, then by name
	sort.Slice(result, func(i, j int) bool {
		if result[i].SortOrder != result[j].SortOrder {
			return result[i].SortOrder < result[j].SortOrder
		}
		return result[i].Name < result[j].Name
	})

	c.JSON(consts.StatusOK, utils.H{"data": result})
}
