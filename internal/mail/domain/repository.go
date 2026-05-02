package domain

import (
	"context"
	"time"
)

type SettingsRepository interface {
	Get(ctx context.Context) (*Settings, error)
	Save(ctx context.Context, settings *Settings) error
}

type VerificationCode struct {
	ID        string
	Email     string
	Purpose   string
	CodeHash  string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

type VerificationCodeRepository interface {
	Create(ctx context.Context, code *VerificationCode) error
	FindLatestValid(ctx context.Context, email, purpose string, now time.Time) (*VerificationCode, error)
	MarkUsed(ctx context.Context, id string, usedAt time.Time) error
}
