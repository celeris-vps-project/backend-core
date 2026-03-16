package infra

import (
	"backend-core/internal/catalog/domain"
	"errors"

	"gorm.io/gorm"
)

type ProductPO struct {
	ID             string `gorm:"primaryKey;column:id"`
	Name           string `gorm:"column:name"`
	Slug           string `gorm:"uniqueIndex;column:slug"`
	Location       string `gorm:"column:location"`
	RegionID       string `gorm:"index;column:region_id"`
	ResourcePoolID string `gorm:"index;column:resource_pool_id"`
	CPU            int    `gorm:"column:cpu"`
	MemoryMB       int    `gorm:"column:memory_mb"`
	DiskGB         int    `gorm:"column:disk_gb"`
	BandwidthGB    int    `gorm:"column:bandwidth_gb"`
	PriceAmount    int64  `gorm:"column:price_amount"`
	Currency       string `gorm:"column:currency"`
	BillingCycle   string `gorm:"column:billing_cycle"`
	Enabled        bool   `gorm:"column:enabled"`
	SortOrder      int    `gorm:"column:sort_order"`
	TotalSlots     int    `gorm:"column:total_slots;default:0"`
	SoldSlots      int    `gorm:"column:sold_slots;default:0"`
}

func (ProductPO) TableName() string { return "products" }

// GormProductRepo implements domain.ProductRepository using GORM.
// It is driver-agnostic: works with SQLite, PostgreSQL, or any GORM-supported database.
type GormProductRepo struct{ db *gorm.DB }

func NewGormProductRepo(db *gorm.DB) *GormProductRepo { return &GormProductRepo{db: db} }

func (r *GormProductRepo) GetByID(id string) (*domain.Product, error) {
	var po ProductPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("product not found")
		}
		return nil, err
	}
	return productToDomain(po), nil
}

func (r *GormProductRepo) GetBySlug(slug string) (*domain.Product, error) {
	var po ProductPO
	if err := r.db.Where("slug = ?", slug).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("product not found")
		}
		return nil, err
	}
	return productToDomain(po), nil
}

func (r *GormProductRepo) ListAll() ([]*domain.Product, error) {
	var pos []ProductPO
	if err := r.db.Order("sort_order ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	return mapProducts(pos), nil
}

func (r *GormProductRepo) ListEnabled() ([]*domain.Product, error) {
	var pos []ProductPO
	if err := r.db.Where("enabled = ?", true).Order("sort_order ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	return mapProducts(pos), nil
}

func (r *GormProductRepo) ListByRegionID(regionID string) ([]*domain.Product, error) {
	var pos []ProductPO
	if err := r.db.Where("region_id = ?", regionID).Order("sort_order ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	return mapProducts(pos), nil
}

func (r *GormProductRepo) Save(p *domain.Product) error {
	po := productFromDomain(p)
	return r.db.Save(&po).Error
}

// ConsumeSlotAtomic atomically increments sold_slots using a conditional UPDATE.
// This eliminates the read-modify-write race condition that occurs under
// concurrent purchase requests (two goroutines reading sold_slots=5, both
// writing sold_slots=6 → one sale is lost).
//
// SQL: UPDATE products SET sold_slots = sold_slots + 1
//
//	WHERE id = ? AND enabled = true
//	AND (total_slots = -1 OR sold_slots < total_slots)
//
// If RowsAffected == 0, either the product doesn't exist, is disabled,
// or has no available slots.
func (r *GormProductRepo) ConsumeSlotAtomic(productID string) error {
	result := r.db.Model(&ProductPO{}).
		Where("id = ? AND enabled = ? AND (total_slots = -1 OR sold_slots < total_slots)", productID, true).
		Update("sold_slots", gorm.Expr("sold_slots + 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("domain_error: no available slots (atomic check)")
	}
	return nil
}

// ReleaseSlotAtomic atomically decrements sold_slots using a conditional UPDATE.
func (r *GormProductRepo) ReleaseSlotAtomic(productID string) error {
	result := r.db.Model(&ProductPO{}).
		Where("id = ? AND sold_slots > 0", productID).
		Update("sold_slots", gorm.Expr("sold_slots - 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("domain_error: no sold slots to release (atomic check)")
	}
	return nil
}

func mapProducts(pos []ProductPO) []*domain.Product {
	out := make([]*domain.Product, len(pos))
	for i, po := range pos {
		out[i] = productToDomain(po)
	}
	return out
}

func productToDomain(po ProductPO) *domain.Product {
	return domain.ReconstituteProduct(
		po.ID, po.Name, po.Slug, po.Location, po.RegionID, po.ResourcePoolID,
		po.CPU, po.MemoryMB, po.DiskGB, po.BandwidthGB,
		po.PriceAmount, po.Currency, domain.BillingCycle(po.BillingCycle),
		po.Enabled, po.SortOrder, po.TotalSlots, po.SoldSlots,
	)
}

func productFromDomain(p *domain.Product) ProductPO {
	return ProductPO{
		ID: p.ID(), Name: p.Name(), Slug: p.Slug(), Location: p.Location(),
		RegionID: p.RegionID(), ResourcePoolID: p.ResourcePoolID(),
		CPU: p.CPU(), MemoryMB: p.MemoryMB(), DiskGB: p.DiskGB(), BandwidthGB: p.BandwidthGB(),
		PriceAmount: p.PriceAmount(), Currency: p.Currency(),
		BillingCycle: string(p.BillingCycle()), Enabled: p.Enabled(), SortOrder: p.SortOrder(),
		TotalSlots: p.TotalSlots(), SoldSlots: p.SoldSlots(),
	}
}
