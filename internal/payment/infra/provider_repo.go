package infra

import (
	"backend-core/internal/payment/domain"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// PaymentProviderPO is the GORM persistence object for payment provider configs.
type PaymentProviderPO struct {
	ID        string `gorm:"primaryKey;size:36"`
	Type      string `gorm:"size:50;not null;index"`
	Name      string `gorm:"size:100;not null"`
	Enabled   bool   `gorm:"default:true"`
	SortOrder int    `gorm:"default:0"`
	Config    string `gorm:"type:text"` // JSON-encoded config
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (PaymentProviderPO) TableName() string { return "payment_providers" }

// GormPaymentProviderRepo implements domain.PaymentProviderRepo using GORM.
type GormPaymentProviderRepo struct {
	db *gorm.DB
}

func NewGormPaymentProviderRepo(db *gorm.DB) *GormPaymentProviderRepo {
	return &GormPaymentProviderRepo{db: db}
}

func (r *GormPaymentProviderRepo) Create(p *domain.PaymentProviderConfig) error {
	cfgJSON, err := json.Marshal(p.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	po := PaymentProviderPO{
		ID:        p.ID,
		Type:      p.Type,
		Name:      p.Name,
		Enabled:   p.Enabled,
		SortOrder: p.SortOrder,
		Config:    string(cfgJSON),
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
	return r.db.Create(&po).Error
}

func (r *GormPaymentProviderRepo) GetByID(id string) (*domain.PaymentProviderConfig, error) {
	var po PaymentProviderPO
	if err := r.db.First(&po, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return poToDomain(&po)
}

func (r *GormPaymentProviderRepo) ListAll() ([]*domain.PaymentProviderConfig, error) {
	var pos []PaymentProviderPO
	if err := r.db.Order("sort_order ASC, created_at ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	return poListToDomain(pos)
}

func (r *GormPaymentProviderRepo) ListEnabled() ([]*domain.PaymentProviderConfig, error) {
	var pos []PaymentProviderPO
	if err := r.db.Where("enabled = ?", true).Order("sort_order ASC, created_at ASC").Find(&pos).Error; err != nil {
		return nil, err
	}
	return poListToDomain(pos)
}

func (r *GormPaymentProviderRepo) Update(p *domain.PaymentProviderConfig) error {
	cfgJSON, err := json.Marshal(p.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return r.db.Model(&PaymentProviderPO{}).Where("id = ?", p.ID).Updates(map[string]interface{}{
		"type":       p.Type,
		"name":       p.Name,
		"enabled":    p.Enabled,
		"sort_order": p.SortOrder,
		"config":     string(cfgJSON),
		"updated_at": time.Now(),
	}).Error
}

func (r *GormPaymentProviderRepo) Delete(id string) error {
	return r.db.Delete(&PaymentProviderPO{}, "id = ?", id).Error
}

// ── helpers ──

func poToDomain(po *PaymentProviderPO) (*domain.PaymentProviderConfig, error) {
	var cfg map[string]interface{}
	if po.Config != "" {
		if err := json.Unmarshal([]byte(po.Config), &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
	}
	return &domain.PaymentProviderConfig{
		ID:        po.ID,
		Type:      po.Type,
		Name:      po.Name,
		Enabled:   po.Enabled,
		SortOrder: po.SortOrder,
		Config:    cfg,
		CreatedAt: po.CreatedAt,
		UpdatedAt: po.UpdatedAt,
	}, nil
}

func poListToDomain(pos []PaymentProviderPO) ([]*domain.PaymentProviderConfig, error) {
	result := make([]*domain.PaymentProviderConfig, 0, len(pos))
	for i := range pos {
		d, err := poToDomain(&pos[i])
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
}
