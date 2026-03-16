package infra

import (
	"backend-core/internal/catalog/domain"
	"fmt"
	"log"

	"golang.org/x/sync/singleflight"
)

// SingleflightProductRepo is a decorator around domain.ProductRepository that
// deduplicates concurrent identical READ queries using singleflight.
//
// When multiple goroutines call the same read method with the same parameters
// concurrently (within the same in-flight window), only ONE actually executes
// the database query. All other callers wait and receive a deep-copied result.
//
// Key properties:
//   - Only READ methods are deduplicated (GetByID, GetBySlug, List*, etc.)
//   - WRITE methods (Save) pass through directly - never deduplicated
//   - Results are deep-copied via domain.ReconstituteProduct to prevent
//     shared-pointer mutations between callers
//   - The singleflight group automatically cleans up after each call completes;
//     there is no TTL or cache - only concurrent in-flight deduplication
type SingleflightProductRepo struct {
	inner domain.ProductRepository
	group singleflight.Group
}

// NewSingleflightProductRepo wraps any ProductRepository implementation with
// singleflight deduplication for read operations.
func NewSingleflightProductRepo(inner domain.ProductRepository) *SingleflightProductRepo {
	log.Printf("[singleflight] product repository decorator enabled")
	return &SingleflightProductRepo{inner: inner}
}

// -- Read operations (deduplicated) --

func (r *SingleflightProductRepo) GetByID(id string) (*domain.Product, error) {
	key := "GetByID:" + id

	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.GetByID(id)
	})
	if err != nil {
		return nil, err
	}

	p := val.(*domain.Product)
	if shared {
		return cloneProduct(p), nil
	}
	return p, nil
}

func (r *SingleflightProductRepo) GetBySlug(slug string) (*domain.Product, error) {
	key := "GetBySlug:" + slug

	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.GetBySlug(slug)
	})
	if err != nil {
		return nil, err
	}

	p := val.(*domain.Product)
	if shared {
		return cloneProduct(p), nil
	}
	return p, nil
}

func (r *SingleflightProductRepo) ListAll() ([]*domain.Product, error) {
	const key = "ListAll"

	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.ListAll()
	})
	if err != nil {
		return nil, err
	}

	products := val.([]*domain.Product)
	if shared {
		return cloneProducts(products), nil
	}
	return products, nil
}

func (r *SingleflightProductRepo) ListEnabled() ([]*domain.Product, error) {
	const key = "ListEnabled"

	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.ListEnabled()
	})
	if err != nil {
		return nil, err
	}

	products := val.([]*domain.Product)
	if shared {
		return cloneProducts(products), nil
	}
	return products, nil
}

func (r *SingleflightProductRepo) ListByRegionID(regionID string) ([]*domain.Product, error) {
	key := "ListByRegionID:" + regionID

	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.ListByRegionID(regionID)
	})
	if err != nil {
		return nil, err
	}

	products := val.([]*domain.Product)
	if shared {
		return cloneProducts(products), nil
	}
	return products, nil
}

// -- Write operations (pass-through, never deduplicated) --

func (r *SingleflightProductRepo) Save(product *domain.Product) error {
	return r.inner.Save(product)
}

// -- Atomic write operations (pass-through, never deduplicated) --

func (r *SingleflightProductRepo) ConsumeSlotAtomic(productID string) error {
	return r.inner.ConsumeSlotAtomic(productID)
}

func (r *SingleflightProductRepo) ReleaseSlotAtomic(productID string) error {
	return r.inner.ReleaseSlotAtomic(productID)
}

// -- Deep-copy helpers --

func cloneProduct(p *domain.Product) *domain.Product {
	return domain.ReconstituteProduct(
		p.ID(), p.Name(), p.Slug(), p.Location(),
		p.RegionID(), p.ResourcePoolID(),
		p.CPU(), p.MemoryMB(), p.DiskGB(), p.BandwidthGB(),
		p.PriceAmount(), p.Currency(), domain.BillingCycle(p.BillingCycle()),
		p.Enabled(), p.SortOrder(), p.TotalSlots(), p.SoldSlots(),
	)
}

func cloneProducts(products []*domain.Product) []*domain.Product {
	out := make([]*domain.Product, len(products))
	for i, p := range products {
		out[i] = cloneProduct(p)
	}
	return out
}

// Compile-time interface check
var _ domain.ProductRepository = (*SingleflightProductRepo)(nil)

// SingleflightStats holds basic info for debugging/metrics endpoints.
type SingleflightStats struct {
	Enabled bool   `json:"enabled"`
	Layer   string `json:"layer"`
	Note    string `json:"note"`
}

// Stats returns a description of the singleflight configuration.
func (r *SingleflightProductRepo) Stats() SingleflightStats {
	return SingleflightStats{
		Enabled: true,
		Layer:   "repository",
		Note:    fmt.Sprintf("wrapping %T", r.inner),
	}
}
