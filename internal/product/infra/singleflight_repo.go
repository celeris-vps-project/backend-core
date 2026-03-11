package infra

import (
	"backend-core/internal/product/domain"
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
// Architecture (Decorator Pattern):
//
//	domain.ProductRepository (interface)
//	         ▲
//	         │ implements
//	┌────────┴──────────────┐
//	│ SingleflightProductRepo│──────┐
//	│  - inner: Repository   │      │ delegates to
//	│  - group: sf.Group     │      ▼
//	└────────────────────────┘  GormProductRepo → DB
//
// Key properties:
//   - Only READ methods are deduplicated (GetByID, GetBySlug, List*, etc.)
//   - WRITE methods (Save) pass through directly — never deduplicated
//   - Results are deep-copied via domain.ReconstituteProduct to prevent
//     shared-pointer mutations between callers (e.g. one caller doing
//     ConsumeSlot() must not affect another caller's copy)
//   - The singleflight group automatically cleans up after each call completes;
//     there is no TTL or cache — only concurrent in-flight deduplication
//
// This protects the database from thundering-herd scenarios:
//   - Adaptive cache MISS during high QPS → hundreds of concurrent DB queries
//   - Without singleflight: all N requests hit the DB simultaneously
//   - With singleflight: 1 request hits the DB, N-1 share the result
//
// Usage in main.go:
//
//	gormRepo := infra.NewGormProductRepo(db)
//	prodRepo := infra.NewSingleflightProductRepo(gormRepo)
//	prodApp  := app.NewProductAppService(prodRepo, ...)
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

// ── Read operations (deduplicated) ──────────────────────────────────────────

// GetByID deduplicates concurrent queries for the same product ID.
// Key: "GetByID:<id>"
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
		// Multiple callers shared this result — deep-copy to prevent
		// one caller's mutations from affecting another.
		return cloneProduct(p), nil
	}
	return p, nil
}

// GetBySlug deduplicates concurrent queries for the same product slug.
// Key: "GetBySlug:<slug>"
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

// ListAll deduplicates concurrent calls to list all products.
// Key: "ListAll" (no parameters — all callers get the same result)
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

// ListEnabled deduplicates concurrent calls to list enabled products.
// This is the HIGHEST traffic method — called on every product catalog page view.
// Key: "ListEnabled"
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

// ListByRegionID deduplicates concurrent queries for the same region.
// Key: "ListByRegionID:<regionID>"
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

// ── Write operations (pass-through, never deduplicated) ─────────────────────

// Save delegates directly to the inner repository. Write operations must
// never be deduplicated — each Save must execute independently.
func (r *SingleflightProductRepo) Save(product *domain.Product) error {
	return r.inner.Save(product)
}

// ── Deep-copy helpers ───────────────────────────────────────────────────────
// These prevent shared-pointer mutations between singleflight callers.
// Uses domain.ReconstituteProduct to rebuild a fresh aggregate from its fields.

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

// ── Compile-time interface check ────────────────────────────────────────────

var _ domain.ProductRepository = (*SingleflightProductRepo)(nil)

// ── Stats (for observability) ───────────────────────────────────────────────

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
