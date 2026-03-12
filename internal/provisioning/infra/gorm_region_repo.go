package infra

import (
	"backend-core/internal/provisioning/domain"
	"errors"

	"gorm.io/gorm"
)

// ---- Persistence Object ----

type RegionPO struct {
	ID       string       `gorm:"primaryKey;column:id"`
	Code     string       `gorm:"uniqueIndex;column:code"`
	Name     string       `gorm:"column:name"`
	FlagIcon string       `gorm:"column:flag_icon"`
	Status   string       `gorm:"column:status"`
	Nodes    []HostNodePO `gorm:"foreignKey:RegionID;references:ID"`
}

func (RegionPO) TableName() string { return "regions" }

// ---- Repository ----

// GormRegionRepo implements domain.RegionRepository using GORM.
// It is driver-agnostic: works with SQLite, PostgreSQL, or any GORM-supported database.
type GormRegionRepo struct{ db *gorm.DB }

func NewGormRegionRepo(db *gorm.DB) *GormRegionRepo {
	return &GormRegionRepo{db: db}
}

func (r *GormRegionRepo) GetByID(id string) (*domain.Region, error) {
	var po RegionPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("region not found")
		}
		return nil, err
	}
	return regionToDomain(po), nil
}

func (r *GormRegionRepo) GetByCode(code string) (*domain.Region, error) {
	var po RegionPO
	if err := r.db.Where("code = ?", code).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("region not found")
		}
		return nil, err
	}
	return regionToDomain(po), nil
}

func (r *GormRegionRepo) ListAll() ([]*domain.Region, error) {
	var pos []RegionPO
	if err := r.db.Find(&pos).Error; err != nil {
		return nil, err
	}
	return regionsToSlice(pos), nil
}

func (r *GormRegionRepo) ListActive() ([]*domain.Region, error) {
	var pos []RegionPO
	if err := r.db.Where("status = ?", domain.RegionStatusActive).Find(&pos).Error; err != nil {
		return nil, err
	}
	return regionsToSlice(pos), nil
}

func (r *GormRegionRepo) Save(region *domain.Region) error {
	po := regionFromDomain(region)
	return r.db.Save(&po).Error
}

// ---- Mapping helpers ----

func regionToDomain(po RegionPO) *domain.Region {
	return domain.ReconstituteRegion(po.ID, po.Code, po.Name, po.FlagIcon, po.Status)
}

func regionFromDomain(r *domain.Region) RegionPO {
	return RegionPO{
		ID:       r.ID(),
		Code:     r.Code(),
		Name:     r.Name(),
		FlagIcon: r.FlagIcon(),
		Status:   r.Status(),
	}
}

func regionsToSlice(pos []RegionPO) []*domain.Region {
	out := make([]*domain.Region, len(pos))
	for i, po := range pos {
		out[i] = regionToDomain(po)
	}
	return out
}
