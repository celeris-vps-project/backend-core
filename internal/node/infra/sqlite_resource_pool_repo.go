package infra

import (
	"backend-core/internal/node/domain"
	"errors"

	"gorm.io/gorm"
)

// ---- Persistence Object ----

type ResourcePoolPO struct {
	ID       string       `gorm:"primaryKey;column:id"`
	Name     string       `gorm:"column:name"`
	RegionID string       `gorm:"index;column:region_id"`
	Status   string       `gorm:"column:status"`
	Nodes    []HostNodePO `gorm:"foreignKey:ResourcePoolID;references:ID"`
}

func (ResourcePoolPO) TableName() string { return "resource_pools" }

// ---- Repository ----

type SqliteResourcePoolRepo struct{ db *gorm.DB }

func NewSqliteResourcePoolRepo(db *gorm.DB) *SqliteResourcePoolRepo {
	return &SqliteResourcePoolRepo{db: db}
}

func (r *SqliteResourcePoolRepo) GetByID(id string) (*domain.ResourcePool, error) {
	var po ResourcePoolPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("resource pool not found")
		}
		return nil, err
	}
	return poolToDomain(po), nil
}

func (r *SqliteResourcePoolRepo) GetByRegionID(regionID string) ([]*domain.ResourcePool, error) {
	var pos []ResourcePoolPO
	if err := r.db.Where("region_id = ?", regionID).Find(&pos).Error; err != nil {
		return nil, err
	}
	return poolsToSlice(pos), nil
}

func (r *SqliteResourcePoolRepo) ListAll() ([]*domain.ResourcePool, error) {
	var pos []ResourcePoolPO
	if err := r.db.Find(&pos).Error; err != nil {
		return nil, err
	}
	return poolsToSlice(pos), nil
}

func (r *SqliteResourcePoolRepo) ListActive() ([]*domain.ResourcePool, error) {
	var pos []ResourcePoolPO
	if err := r.db.Where("status = ?", domain.PoolStatusActive).Find(&pos).Error; err != nil {
		return nil, err
	}
	return poolsToSlice(pos), nil
}

func (r *SqliteResourcePoolRepo) Save(pool *domain.ResourcePool) error {
	po := poolFromDomain(pool)
	return r.db.Save(&po).Error
}

func (r *SqliteResourcePoolRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&ResourcePoolPO{}).Error
}

// ---- Mapping helpers ----

func poolToDomain(po ResourcePoolPO) *domain.ResourcePool {
	return domain.ReconstituteResourcePool(po.ID, po.Name, po.RegionID, po.Status)
}

func poolFromDomain(p *domain.ResourcePool) ResourcePoolPO {
	return ResourcePoolPO{
		ID:       p.ID(),
		Name:     p.Name(),
		RegionID: p.RegionID(),
		Status:   p.Status(),
	}
}

func poolsToSlice(pos []ResourcePoolPO) []*domain.ResourcePool {
	out := make([]*domain.ResourcePool, len(pos))
	for i, po := range pos {
		out[i] = poolToDomain(po)
	}
	return out
}
