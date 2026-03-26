package infra

import (
	"backend-core/internal/catalog/domain"
	"backend-core/pkg/bloom"
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
)

// ─────────────────────────────────────────────────────────────────────────────
// BloomProductRepo — Bloom filter decorator for cache penetration protection
// ─────────────────────────────────────────────────────────────────────────────
//
// This decorator is the outermost layer in the repository decorator chain:
//
//   Request → **Bloom** → Singleflight → Gorm → Database
//
// For single-key lookups (GetByID, GetBySlug), it first checks the Bloom
// filter. If the key is DEFINITELY NOT in the set, it returns "product not
// found" immediately without hitting the database.
//
// This prevents cache penetration attacks where an attacker sends a flood
// of requests with random non-existent IDs, bypassing all cache layers and
// hammering the database directly.
//
// Properties:
//   - Zero false negatives: if a product exists, it will always be found
//   - ~1% false positives (configurable): a small fraction of non-existent
//     IDs may still reach the DB — this is acceptable
//   - Warm-up: on construction, loads all existing product IDs and slugs
//   - Write-through: Save() automatically adds new IDs/slugs to the filter
//   - Rebuild: call Rebuild() to reconstruct the filter from scratch
//   - Thread-safe: the underlying bloom.Filter uses sync.RWMutex

var ErrBloomNotFound = errors.New("product not found")

type BloomProductRepo struct {
	inner      domain.ProductRepository
	idFilter   *bloom.Filter // Bloom filter for product IDs
	slugFilter *bloom.Filter // Bloom filter for product slugs
	blocked    int64         // atomic counter of requests blocked by bloom
}

// NewBloomProductRepo wraps a ProductRepository with Bloom filter protection.
// It loads all existing products from the inner repo to warm up the filter.
//
// Parameters:
//   - inner: the underlying repository (typically GormProductRepo)
//   - expectedN: expected maximum number of products (e.g. 10000)
//   - fpRate: desired false positive rate (e.g. 0.01 for 1%)
func NewBloomProductRepo(inner domain.ProductRepository, expectedN int, fpRate float64) *BloomProductRepo {
	r := &BloomProductRepo{
		inner:      inner,
		idFilter:   bloom.New(expectedN, fpRate),
		slugFilter: bloom.New(expectedN, fpRate),
	}

	// Warm up: load all existing products into the Bloom filters
	if err := r.warmUp(); err != nil {
		log.Printf("[bloom] WARNING: failed to warm up bloom filter: %v (filter starts empty)", err)
	}

	return r
}

// warmUp loads all products from the database and adds their IDs and slugs
// to the Bloom filters. Called once during construction.
func (r *BloomProductRepo) warmUp() error {
	products, err := r.inner.ListAll(context.Background())
	if err != nil {
		return err
	}

	for _, p := range products {
		r.idFilter.Add(p.ID())
		if slug := p.Slug(); slug != "" {
			r.slugFilter.Add(slug)
		}
	}

	log.Printf("[bloom] product bloom filter warmed up: %d products loaded (ID filter: %d bits / %.1f KB, Slug filter: %d bits / %.1f KB)",
		len(products),
		r.idFilter.BitSize(), float64(r.idFilter.BitSize())/8/1024,
		r.slugFilter.BitSize(), float64(r.slugFilter.BitSize())/8/1024,
	)
	return nil
}

// ── Read operations (Bloom-protected) ──────────────────────────────────────

func (r *BloomProductRepo) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	if !r.idFilter.Test(id) {
		atomic.AddInt64(&r.blocked, 1)
		return nil, ErrBloomNotFound
	}
	return r.inner.GetByID(ctx, id)
}

func (r *BloomProductRepo) GetBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	if !r.slugFilter.Test(slug) {
		atomic.AddInt64(&r.blocked, 1)
		return nil, ErrBloomNotFound
	}
	return r.inner.GetBySlug(ctx, slug)
}

// ── List operations (pass-through, no Bloom needed) ────────────────────────

func (r *BloomProductRepo) ListAll(ctx context.Context) ([]*domain.Product, error) {
	return r.inner.ListAll(ctx)
}

func (r *BloomProductRepo) ListEnabled(ctx context.Context) ([]*domain.Product, error) {
	return r.inner.ListEnabled(ctx)
}

func (r *BloomProductRepo) ListByRegionID(ctx context.Context, regionID string) ([]*domain.Product, error) {
	return r.inner.ListByRegionID(ctx, regionID)
}

// ── Write operations (write-through: save + add to Bloom) ──────────────────

func (r *BloomProductRepo) Save(ctx context.Context, product *domain.Product) error {
	if err := r.inner.Save(ctx, product); err != nil {
		return err
	}
	// Add the new/updated product's ID and slug to the Bloom filter
	// so subsequent reads won't be falsely rejected.
	r.idFilter.Add(product.ID())
	if slug := product.Slug(); slug != "" {
		r.slugFilter.Add(slug)
	}
	return nil
}

// ── Atomic operations (pass-through) ───────────────────────────────────────

func (r *BloomProductRepo) ConsumeSlotAtomic(ctx context.Context, productID string) error {
	return r.inner.ConsumeSlotAtomic(ctx, productID)
}

func (r *BloomProductRepo) ReleaseSlotAtomic(ctx context.Context, productID string) error {
	return r.inner.ReleaseSlotAtomic(ctx, productID)
}

// ── Maintenance ────────────────────────────────────────────────────────────

// Rebuild reconstructs the Bloom filters from scratch by re-reading all
// products from the database. This is useful if products have been deleted
// directly from the database (standard Bloom filters don't support removal).
func (r *BloomProductRepo) Rebuild() error {
	r.idFilter.Reset()
	r.slugFilter.Reset()
	atomic.StoreInt64(&r.blocked, 0)
	return r.warmUp()
}

// Blocked returns the total number of requests rejected by the Bloom filter
// (confirmed non-existent keys that never reached the database).
func (r *BloomProductRepo) Blocked() int64 {
	return atomic.LoadInt64(&r.blocked)
}

// BloomStats holds diagnostic information about the Bloom filter state.
type BloomStats struct {
	Enabled       bool   `json:"enabled"`
	IDElements    int64  `json:"id_elements"`
	SlugElements  int64  `json:"slug_elements"`
	IDBitSize     uint64 `json:"id_bit_size"`
	SlugBitSize   uint64 `json:"slug_bit_size"`
	HashFunctions uint64 `json:"hash_functions"`
	MemoryKB      string `json:"memory_kb"`
	Blocked       int64  `json:"blocked"`
}

// Stats returns diagnostic information about the Bloom filter.
func (r *BloomProductRepo) Stats() BloomStats {
	totalBits := r.idFilter.BitSize() + r.slugFilter.BitSize()
	return BloomStats{
		Enabled:       true,
		IDElements:    r.idFilter.Count(),
		SlugElements:  r.slugFilter.Count(),
		IDBitSize:     r.idFilter.BitSize(),
		SlugBitSize:   r.slugFilter.BitSize(),
		HashFunctions: r.idFilter.HashCount(),
		MemoryKB:      fmt.Sprintf("%.1f", float64(totalBits)/8/1024),
		Blocked:       r.Blocked(),
	}
}

// Compile-time interface check
var _ domain.ProductRepository = (*BloomProductRepo)(nil)
