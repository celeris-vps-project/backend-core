package infra

import (
	"backend-core/internal/promotion/app"
	"backend-core/internal/promotion/domain"
	"backend-core/pkg/bloom"
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

var ErrBloomNotFound = errors.New("coupon not found")

type BloomCouponRepo struct {
	inner      *GormCouponRepo
	idFilter   *bloom.Filter
	codeFilter *bloom.Filter
	blocked    int64 // atomic counter of requests blocked by bloom
}

func (r *BloomCouponRepo) Create(ctx context.Context, coupon *domain.Coupon, allowedProductIDs []string) error {
	if err := r.inner.Create(ctx, coupon, allowedProductIDs); err != nil {
		return err
	}
	r.idFilter.Add(coupon.ID)
	r.codeFilter.Add(coupon.Code)
	return nil
}

func (r *BloomCouponRepo) GetByID(ctx context.Context, id string) (*app.CouponWithProducts, error) {
	if !r.idFilter.Test(id) {
		return nil, ErrBloomNotFound
	}
	return r.inner.GetByID(ctx, id)
}

func (r *BloomCouponRepo) List(ctx context.Context) ([]app.CouponWithProducts, error) {
	return r.inner.List(ctx)
}

func (r *BloomCouponRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return r.inner.SetEnabled(ctx, id, enabled)
}

func (r *BloomCouponRepo) FindRedemptionByOrder(ctx context.Context, orderID string) (*domain.Redemption, error) {
	return r.inner.FindRedemptionByOrder(ctx, orderID)
}

func (r *BloomCouponRepo) Redeem(ctx context.Context, req app.RedeemCouponRequest, now time.Time) (*domain.Redemption, error) {
	return r.inner.Redeem(ctx, req, now)
}

func (r *BloomCouponRepo) GetByCodeWithProductID(ctx context.Context, code, productID string) (*domain.Coupon, error) {
	if !r.codeFilter.Test(code) {
		return nil, ErrBloomNotFound
	}
	return r.inner.GetByCodeWithProductID(ctx, code, productID)
}

func (r *BloomCouponRepo) CountUserCouponRedemptions(ctx context.Context, userID string, couponID string) (int64, error) {
	return r.inner.CountUserCouponRedemptions(ctx, userID, couponID)
}

func NewBloomCouponRepo(inner *GormCouponRepo, expectedN int, fpRate float64) *BloomCouponRepo {
	r := &BloomCouponRepo{
		inner:      inner,
		idFilter:   bloom.New(expectedN, fpRate),
		codeFilter: bloom.New(expectedN, fpRate),
	}

	// Warm up: load all existing products into the Bloom filters
	if err := r.warmUp(); err != nil {
		log.Printf("[bloom] WARNING: failed to warm up bloom filter: %v (filter starts empty)", err)
	}

	return r
}

// warmUp loads all products from the database and adds their IDs and slugs
// to the Bloom filters. Called once during construction.
func (r *BloomCouponRepo) warmUp() error {
	coupons, err := r.inner.List(context.Background())
	if err != nil {
		return err
	}

	for _, c := range coupons {
		if !c.Coupon.Enabled {
			continue
		}

		r.idFilter.Add(c.Coupon.ID)
		if code := c.Coupon.Code; code != "" {
			r.codeFilter.Add(code)
		}
	}

	log.Printf("[bloom] product bloom filter warmed up: %d products loaded (ID filter: %d bits / %.1f KB, Slug filter: %d bits / %.1f KB)",
		len(coupons),
		r.idFilter.BitSize(), float64(r.idFilter.BitSize())/8/1024,
		r.codeFilter.BitSize(), float64(r.codeFilter.BitSize())/8/1024,
	)
	return nil
}

// ── Maintenance ────────────────────────────────────────────────────────────

// Rebuild reconstructs the Bloom filters from scratch by re-reading all
// products from the database. This is useful if products have been deleted
// directly from the database (standard Bloom filters don't support removal).
func (r *BloomCouponRepo) Rebuild() error {
	r.idFilter.Reset()
	r.codeFilter.Reset()
	atomic.StoreInt64(&r.blocked, 0)
	return r.warmUp()
}

// Blocked returns the total number of requests rejected by the Bloom filter
// (confirmed non-existent keys that never reached the database).
func (r *BloomCouponRepo) Blocked() int64 {
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
func (r *BloomCouponRepo) Stats() BloomStats {
	totalBits := r.idFilter.BitSize() + r.codeFilter.BitSize()
	return BloomStats{
		Enabled:       true,
		IDElements:    r.idFilter.Count(),
		SlugElements:  r.codeFilter.Count(),
		IDBitSize:     r.idFilter.BitSize(),
		SlugBitSize:   r.codeFilter.BitSize(),
		HashFunctions: r.idFilter.HashCount(),
		MemoryKB:      fmt.Sprintf("%.1f", float64(totalBits)/8/1024),
		Blocked:       r.Blocked(),
	}
}

// Compile-time interface check
var _ app.CouponRepository = (*BloomCouponRepo)(nil)
