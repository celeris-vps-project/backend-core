package infra

import (
	"backend-core/internal/node/domain"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ---- Persistence Object ----

type BootstrapTokenPO struct {
	ID           string     `gorm:"primaryKey;column:id"`
	NodeID       string     `gorm:"column:node_id;index"` // the host node this token is for
	Token        string     `gorm:"uniqueIndex;column:token"`
	ExpiresAt    time.Time  `gorm:"column:expires_at"`
	Used         bool       `gorm:"column:used;default:false"`
	UsedByNodeID string     `gorm:"column:used_by_node_id"`
	UsedAt       *time.Time `gorm:"column:used_at"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	Description  string     `gorm:"column:description"`
}

func (BootstrapTokenPO) TableName() string { return "bootstrap_tokens" }

// ---- Repository ----

// GormBootstrapTokenRepo implements domain.BootstrapTokenRepository using GORM.
// It is driver-agnostic: works with SQLite, PostgreSQL, or any GORM-supported database.
type GormBootstrapTokenRepo struct{ db *gorm.DB }

func NewGormBootstrapTokenRepo(db *gorm.DB) *GormBootstrapTokenRepo {
	return &GormBootstrapTokenRepo{db: db}
}

func (r *GormBootstrapTokenRepo) GetByToken(token string) (*domain.BootstrapToken, error) {
	var po BootstrapTokenPO
	if err := r.db.Where("token = ?", token).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("bootstrap token not found")
		}
		return nil, err
	}
	return btToDomain(po), nil
}

func (r *GormBootstrapTokenRepo) GetByID(id string) (*domain.BootstrapToken, error) {
	var po BootstrapTokenPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("bootstrap token not found")
		}
		return nil, err
	}
	return btToDomain(po), nil
}

func (r *GormBootstrapTokenRepo) ListAll() ([]*domain.BootstrapToken, error) {
	var pos []BootstrapTokenPO
	if err := r.db.Order("created_at DESC").Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.BootstrapToken, len(pos))
	for i, po := range pos {
		out[i] = btToDomain(po)
	}
	return out, nil
}

func (r *GormBootstrapTokenRepo) Save(bt *domain.BootstrapToken) error {
	po := btFromDomain(bt)
	return r.db.Save(&po).Error
}

func (r *GormBootstrapTokenRepo) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&BootstrapTokenPO{}).Error
}

// ---- Mapping helpers ----

func btToDomain(po BootstrapTokenPO) *domain.BootstrapToken {
	return domain.ReconstituteBootstrapToken(
		po.ID, po.NodeID, po.Token, po.ExpiresAt,
		po.Used, po.UsedByNodeID, po.UsedAt,
		po.CreatedAt, po.Description,
	)
}

func btFromDomain(bt *domain.BootstrapToken) BootstrapTokenPO {
	return BootstrapTokenPO{
		ID: bt.ID(), NodeID: bt.NodeID(), Token: bt.Token(), ExpiresAt: bt.ExpiresAt(),
		Used: bt.Used(), UsedByNodeID: bt.UsedByNodeID(), UsedAt: bt.UsedAt(),
		CreatedAt: bt.CreatedAt(), Description: bt.Description(),
	}
}
