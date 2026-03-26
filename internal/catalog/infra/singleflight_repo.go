package infra

import (
	"backend-core/internal/catalog/domain"
	"context"
	"fmt"
	"log"

	"golang.org/x/sync/singleflight"
)

// SingleflightProductRepo is a decorator around domain.ProductRepository that
// deduplicates concurrent identical READ queries using singleflight.
type SingleflightProductRepo struct {
	inner domain.ProductRepository
	group singleflight.Group
}

func NewSingleflightProductRepo(inner domain.ProductRepository) *SingleflightProductRepo {
	log.Printf("[singleflight] product repository decorator enabled")
	return &SingleflightProductRepo{inner: inner}
}

func (r *SingleflightProductRepo) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	key := "GetByID:" + id
	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.GetByID(ctx, id)
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

func (r *SingleflightProductRepo) GetBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	key := "GetBySlug:" + slug
	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.GetBySlug(ctx, slug)
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

func (r *SingleflightProductRepo) ListAll(ctx context.Context) ([]*domain.Product, error) {
	const key = "ListAll"
	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.ListAll(ctx)
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

func (r *SingleflightProductRepo) ListEnabled(ctx context.Context) ([]*domain.Product, error) {
	const key = "ListEnabled"
	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.ListEnabled(ctx)
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

func (r *SingleflightProductRepo) ListByRegionID(ctx context.Context, regionID string) ([]*domain.Product, error) {
	key := "ListByRegionID:" + regionID
	val, err, shared := r.group.Do(key, func() (interface{}, error) {
		return r.inner.ListByRegionID(ctx, regionID)
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

func (r *SingleflightProductRepo) Save(ctx context.Context, product *domain.Product) error {
	return r.inner.Save(ctx, product)
}

func (r *SingleflightProductRepo) ConsumeSlotAtomic(ctx context.Context, productID string) error {
	return r.inner.ConsumeSlotAtomic(ctx, productID)
}

func (r *SingleflightProductRepo) ReleaseSlotAtomic(ctx context.Context, productID string) error {
	return r.inner.ReleaseSlotAtomic(ctx, productID)
}

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

var _ domain.ProductRepository = (*SingleflightProductRepo)(nil)

type SingleflightStats struct {
	Enabled bool   `json:"enabled"`
	Layer   string `json:"layer"`
	Note    string `json:"note"`
}

func (r *SingleflightProductRepo) Stats() SingleflightStats {
	return SingleflightStats{
		Enabled: true,
		Layer:   "repository",
		Note:    fmt.Sprintf("wrapping %T", r.inner),
	}
}
