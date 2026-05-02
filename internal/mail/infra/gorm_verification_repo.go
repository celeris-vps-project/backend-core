package infra

import (
	"backend-core/internal/mail/domain"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type MailVerificationCodePO struct {
	ID        string     `gorm:"primaryKey;size:36"`
	Email     string     `gorm:"size:255;not null;index:idx_mail_code_lookup,priority:1"`
	Purpose   string     `gorm:"size:32;not null;index:idx_mail_code_lookup,priority:2"`
	CodeHash  string     `gorm:"size:64;not null"`
	ExpiresAt time.Time  `gorm:"not null;index"`
	UsedAt    *time.Time `gorm:"index"`
	CreatedAt time.Time  `gorm:"index"`
}

func (MailVerificationCodePO) TableName() string { return "mail_verification_codes" }

type GormVerificationCodeRepo struct {
	db *gorm.DB
}

func NewGormVerificationCodeRepo(db *gorm.DB) *GormVerificationCodeRepo {
	return &GormVerificationCodeRepo{db: db}
}

func (r *GormVerificationCodeRepo) Create(ctx context.Context, code *domain.VerificationCode) error {
	if code == nil {
		return errors.New("verification code is nil")
	}
	po := MailVerificationCodePO{
		ID:        code.ID,
		Email:     code.Email,
		Purpose:   code.Purpose,
		CodeHash:  code.CodeHash,
		ExpiresAt: code.ExpiresAt,
		UsedAt:    code.UsedAt,
		CreatedAt: code.CreatedAt,
	}
	return r.db.WithContext(ctx).Create(&po).Error
}

func (r *GormVerificationCodeRepo) FindLatestValid(ctx context.Context, email, purpose string, now time.Time) (*domain.VerificationCode, error) {
	var po MailVerificationCodePO
	err := r.db.WithContext(ctx).
		Where("email = ? AND purpose = ? AND used_at IS NULL AND expires_at > ?", email, purpose, now).
		Order("created_at DESC").
		First(&po).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrInvalidVerificationCode
		}
		return nil, err
	}
	return &domain.VerificationCode{
		ID:        po.ID,
		Email:     po.Email,
		Purpose:   po.Purpose,
		CodeHash:  po.CodeHash,
		ExpiresAt: po.ExpiresAt,
		UsedAt:    po.UsedAt,
		CreatedAt: po.CreatedAt,
	}, nil
}

func (r *GormVerificationCodeRepo) MarkUsed(ctx context.Context, id string, usedAt time.Time) error {
	result := r.db.WithContext(ctx).Model(&MailVerificationCodePO{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", usedAt)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrInvalidVerificationCode
	}
	return nil
}
