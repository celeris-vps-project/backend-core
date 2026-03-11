package infra

import (
	"backend-core/internal/node/domain"
	"errors"

	"gorm.io/gorm"
)

// ---- Persistence Object ----

type ResourcePoolPO struct {
	ID          string       `gorm:"primaryKey;column:id"`
	Name        string       `gorm:"column:name"`
	RegionID    string       `gorm:"index;column:region_id"`
	Status      string       `gorm:"column:status"`
	Description string       `gorm:"column:description"`
	SortOrder   int          `gorm:"column:sort_order;default:0"`
	Nodes       []HostNodePO `gorm:"foreignKey:ResourcePoolID;references:ID"`
}

func (ResourcePoolPO) TableName() string { return "resource_pools" }

// ---- Repository ----

// GormResourcePoolRepo implements domain.ResourcePoolRepository using GORM.
// It is driver-agnostic: works with SQLite, PostgreSQL, or any GORM-supported database.
type GormResourcePoolRepo struct{ db *gorm.DB }

func NewGormResourcePoolRepo(db *gorm.DB) *GormResourcePoolRepo {
	return &GormResourcePoolRepo{db: db}
}

func (r *GormResourcePoolRepo) GetByID(id string) (*domain.ResourcePool, error) {
	var po ResourcePoolPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("resource pool not found")
		}
		return nil, err
	}
	return poolToDomain(po), nil
}

func (r *GormResourcePoolRepo) GetByRegionID(regionID string) ([]*domain.ResourcePool, error) {
	var pos []ResourcePoolPO
	if err := r.db.Where("region_id = ?", regionID).Find(&pos).Error; err != nil {
		return nil, err
	}
	return poolsToSlice(pos), nil
}

func (r *GormResourcePoolRepo) ListAll() ([]*domain.ResourcePool, error) {
	var pos []ResourcePoolPO
	if err := r.db.Find(&pos).Error; err != nil {
		return nil, err
	}
	return poolsToSlice(pos), nil
}

func (r *GormResourcePoolRepo) ListActive() ([]*domain.ResourcePool, error) {
	var pos []ResourcePoolPO
	if err := r.db.Where("status = ?", domain.PoolStatusActive).Find(&pos).Error; err != nil {
		return nil, err
	}
	return poolsToSlice(pos), nil
}

func (r *GormResourcePoolRepo) Save(pool *domain.ResourcePool) error {
	po := poolFromDomain(pool)
	return r.db.Save(&po).Error
}

func (r *GormResourcePoolRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&ResourcePoolPO{}).Error
}

// ---- Mapping helpers ----

func poolToDomain(po ResourcePoolPO) *domain.ResourcePool {
	return domain.ReconstituteResourcePool(po.ID, po.Name, po.RegionID, po.Status, po.Description, po.SortOrder)
}

func poolFromDomain(p *domain.ResourcePool) ResourcePoolPO {
	return ResourcePoolPO{
		ID:          p.ID(),
		Name:        p.Name(),
		RegionID:    p.RegionID(),
		Status:      p.Status(),
		Description: p.Description(),
		SortOrder:   p.SortOrder(),
	}
}

func poolsToSlice(pos []ResourcePoolPO) []*domain.ResourcePool {
	out := make([]*domain.ResourcePool, len(pos))
	for i, po := range pos {
		out[i] = poolToDomain(po)
	}
	return out
}
