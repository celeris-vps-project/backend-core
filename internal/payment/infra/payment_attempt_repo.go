package infra

import (
	"backend-core/internal/payment/domain"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type PaymentAttemptPO struct {
	ID         string `gorm:"primaryKey;size:64"`
	OrderID    string `gorm:"index;size:64;not null"`
	ProviderID string `gorm:"index;size:64;not null"`
	PayType    string `gorm:"index;size:32"`
	TradeNo    string `gorm:"index;size:128"`
	OutTradeNo string `gorm:"index;size:128;not null"`
	PayURL     string `gorm:"type:text"`
	Status     string `gorm:"index;size:32;not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (PaymentAttemptPO) TableName() string { return "payment_attempts" }

type GormPaymentAttemptRepo struct {
	db *gorm.DB
}

func NewGormPaymentAttemptRepo(db *gorm.DB) *GormPaymentAttemptRepo {
	return &GormPaymentAttemptRepo{db: db}
}

func (r *GormPaymentAttemptRepo) Create(ctx context.Context, attempt *domain.PaymentAttempt) error {
	if attempt == nil {
		return nil
	}
	now := time.Now()
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = now
	}
	attempt.UpdatedAt = now
	return r.db.WithContext(ctx).Create(paymentAttemptFromDomain(attempt)).Error
}

func (r *GormPaymentAttemptRepo) Update(ctx context.Context, attempt *domain.PaymentAttempt) error {
	if attempt == nil {
		return nil
	}
	attempt.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Save(paymentAttemptFromDomain(attempt)).Error
}

func (r *GormPaymentAttemptRepo) FindLatestByOrderID(ctx context.Context, orderID string) (*domain.PaymentAttempt, error) {
	var po PaymentAttemptPO
	if err := r.db.WithContext(ctx).
		Where("order_id = ?", orderID).
		Order("created_at DESC").
		First(&po).Error; err != nil {
		return nil, mapPaymentAttemptErr(err)
	}
	return paymentAttemptToDomain(&po), nil
}

func (r *GormPaymentAttemptRepo) FindByOutTradeNo(ctx context.Context, outTradeNo string) (*domain.PaymentAttempt, error) {
	var po PaymentAttemptPO
	if err := r.db.WithContext(ctx).
		Where("out_trade_no = ?", outTradeNo).
		Order("created_at DESC").
		First(&po).Error; err != nil {
		return nil, mapPaymentAttemptErr(err)
	}
	return paymentAttemptToDomain(&po), nil
}

func (r *GormPaymentAttemptRepo) ListPending(ctx context.Context, limit int) ([]*domain.PaymentAttempt, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []PaymentAttemptPO
	if err := r.db.WithContext(ctx).
		Where("status = ?", domain.ChargeStatusPending).
		Order("updated_at ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	attempts := make([]*domain.PaymentAttempt, 0, len(rows))
	for i := range rows {
		attempts = append(attempts, paymentAttemptToDomain(&rows[i]))
	}
	return attempts, nil
}

func mapPaymentAttemptErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrPaymentAttemptNotFound
	}
	return err
}

func paymentAttemptFromDomain(attempt *domain.PaymentAttempt) *PaymentAttemptPO {
	return &PaymentAttemptPO{
		ID:         attempt.ID,
		OrderID:    attempt.OrderID,
		ProviderID: attempt.ProviderID,
		PayType:    attempt.PayType,
		TradeNo:    attempt.TradeNo,
		OutTradeNo: attempt.OutTradeNo,
		PayURL:     attempt.PayURL,
		Status:     attempt.Status,
		CreatedAt:  attempt.CreatedAt,
		UpdatedAt:  attempt.UpdatedAt,
	}
}

func paymentAttemptToDomain(po *PaymentAttemptPO) *domain.PaymentAttempt {
	return &domain.PaymentAttempt{
		ID:         po.ID,
		OrderID:    po.OrderID,
		ProviderID: po.ProviderID,
		PayType:    po.PayType,
		TradeNo:    po.TradeNo,
		OutTradeNo: po.OutTradeNo,
		PayURL:     po.PayURL,
		Status:     po.Status,
		CreatedAt:  po.CreatedAt,
		UpdatedAt:  po.UpdatedAt,
	}
}

var _ domain.PaymentAttemptRepo = (*GormPaymentAttemptRepo)(nil)
