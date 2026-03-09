package infra

import (
	"backend-core/internal/product/domain"
	"errors"
	"time"

	"gorm.io/gorm"
)

// GroupPO is the GORM persistence object for the groups table.
type GroupPO struct {
	ID          string    `gorm:"primaryKey;column:id"`
	Name        string    `gorm:"column:name"`
	Description string    `gorm:"column:description"`
	SortOrder   int       `gorm:"column:sort_order;default:0"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (GroupPO) TableName() string { return "groups" }

// SqliteGroupRepo implements domain.GroupRepository using GORM + SQLite.
type SqliteGroupRepo struct{ db *gorm.DB }

func NewSqliteGroupRepo(db *gorm.DB) *SqliteGroupRepo { return &SqliteGroupRepo{db: db} }

func (r *SqliteGroupRepo) GetByID(id string) (*domain.Group, error) {
	var po GroupPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("group not found")
		}
		return nil, err
	}
	return groupToDomain(po), nil
}

func (r *SqliteGroupRepo) ListAll() ([]*domain.Group, error) {
	var pos []GroupPO
	if err := r.db.Order("sort_order ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.Group, len(pos))
	for i, po := range pos {
		out[i] = groupToDomain(po)
	}
	return out, nil
}

func (r *SqliteGroupRepo) Save(g *domain.Group) error {
	po := groupFromDomain(g)
	return r.db.Save(&po).Error
}

func (r *SqliteGroupRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&GroupPO{}).Error
}

// ---- Mapping helpers ----

func groupToDomain(po GroupPO) *domain.Group {
	return domain.ReconstituteGroup(
		po.ID, po.Name, po.Description, po.SortOrder,
		po.CreatedAt, po.UpdatedAt,
	)
}

func groupFromDomain(g *domain.Group) GroupPO {
	return GroupPO{
		ID:          g.ID(),
		Name:        g.Name(),
		Description: g.Description(),
		SortOrder:   g.SortOrder(),
		CreatedAt:   g.CreatedAt(),
		UpdatedAt:   g.UpdatedAt(),
	}
}
