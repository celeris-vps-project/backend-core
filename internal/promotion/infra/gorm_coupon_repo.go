package infra

import (
	"backend-core/internal/promotion/app"
	"backend-core/internal/promotion/domain"
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

type CouponPO struct {
	ID             string     `gorm:"primaryKey;column:id"`
	Code           string     `gorm:"uniqueIndex;column:code"`
	DiscountType   string     `gorm:"column:discount_type"`
	DiscountValue  int64      `gorm:"column:discount_value"`
	StartsAt       *time.Time `gorm:"column:starts_at"`
	ExpiresAt      *time.Time `gorm:"column:expires_at"`
	MaxRedemptions int        `gorm:"column:max_redemptions"`
	UsedCount      int        `gorm:"column:used_count;default:0"`
	PerUserLimit   int        `gorm:"column:per_user_limit;default:1"`
	Enabled        bool       `gorm:"column:enabled;default:true"`
	Description    string     `gorm:"column:description"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
}

func (CouponPO) TableName() string { return "coupons" }

type CouponAllowedProductPO struct {
	CouponID  string    `gorm:"primaryKey;column:coupon_id"`
	ProductID string    `gorm:"primaryKey;column:product_id;index"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (CouponAllowedProductPO) TableName() string { return "coupon_allowed_products" }

type CouponRedemptionPO struct {
	ID             string    `gorm:"primaryKey;column:id"`
	CouponID       string    `gorm:"index;column:coupon_id"`
	Code           string    `gorm:"column:code"`
	UserID         string    `gorm:"index;column:user_id"`
	OrderID        string    `gorm:"uniqueIndex;column:order_id"`
	ProductID      string    `gorm:"index;column:product_id"`
	OriginalAmount int64     `gorm:"column:original_amount"`
	DiscountAmount int64     `gorm:"column:discount_amount"`
	FinalAmount    int64     `gorm:"column:final_amount"`
	RedeemedAt     time.Time `gorm:"column:redeemed_at"`
}

func (CouponRedemptionPO) TableName() string { return "coupon_redemptions" }

type GormCouponRepo struct {
	db *gorm.DB
}

func NewGormCouponRepo(db *gorm.DB) *GormCouponRepo {
	return &GormCouponRepo{db: db}
}

func (r *GormCouponRepo) Create(ctx context.Context, coupon *domain.Coupon, allowedProductIDs []string) error {
	po := couponToPO(coupon)
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&po).Error; err != nil {
			return err
		}
		for _, productID := range allowedProductIDs {
			if err := tx.Create(&CouponAllowedProductPO{
				CouponID:  coupon.ID,
				ProductID: productID,
				CreatedAt: now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *GormCouponRepo) GetByID(ctx context.Context, id string) (*app.CouponWithProducts, error) {
	var po CouponPO
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrCouponNotFound
		}
		return nil, err
	}
	products, err := r.allowedProducts(ctx, id)
	if err != nil {
		return nil, err
	}
	return &app.CouponWithProducts{Coupon: couponToDomain(po), AllowedProductIDs: products}, nil
}

func (r *GormCouponRepo) GetByCodeWithProductID(ctx context.Context, code, productID string) (*domain.Coupon, error) {
	var po CouponPO
	if err := r.db.WithContext(ctx).
		Joins("join coupon_allowed_products on coupon_allowed_products.coupon_id = id").
		Where("code = ?", code).
		Where("coupon_allowed_products.product_id = ?", productID).
		Where("enabled = true").
		Where("used_count < max_redemptions").
		First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrCouponNotFound
		}
		return nil, err
	}
	return couponToDomain(po), nil
}

func (r *GormCouponRepo) List(ctx context.Context) ([]app.CouponWithProducts, error) {
	var pos []CouponPO
	if err := r.db.WithContext(ctx).Order("created_at DESC").Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]app.CouponWithProducts, 0, len(pos))
	for _, po := range pos {
		products, err := r.allowedProducts(ctx, po.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, app.CouponWithProducts{
			Coupon:            couponToDomain(po),
			AllowedProductIDs: products,
		})
	}
	return out, nil
}

func (r *GormCouponRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	result := r.db.WithContext(ctx).Model(&CouponPO{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"enabled":    enabled,
			"updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrCouponNotFound
	}
	return nil
}

func (r *GormCouponRepo) FindRedemptionByOrder(ctx context.Context, orderID string) (*domain.Redemption, error) {
	if orderID == "" {
		return nil, nil
	}
	var po CouponRedemptionPO
	if err := r.db.WithContext(ctx).Where("order_id = ?", orderID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	redemption := redemptionToDomain(po)
	return &redemption, nil
}

func (r *GormCouponRepo) CountUserCouponRedemptions(ctx context.Context, userID string, couponID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&CouponRedemptionPO{}).
		Where("user_id = ? AND coupon_id = ?", userID, couponID).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (r *GormCouponRepo) Redeem(ctx context.Context, req app.RedeemCouponRequest, now time.Time) (*domain.Redemption, error) {
	var redemption domain.Redemption
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing CouponRedemptionPO
		if err := tx.Where("order_id = ?", req.OrderID).First(&existing).Error; err == nil {
			redemption = redemptionToDomain(existing)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var couponPO CouponPO
		if err := tx.Where("code = ?", domain.NormalizeCode(req.Code)).First(&couponPO).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrCouponNotFound
			}
			return err
		}
		coupon := couponToDomain(couponPO)
		if err := coupon.ValidateRedeemable(now); err != nil {
			return err
		}

		var allowedCount int64
		if err := tx.Model(&CouponAllowedProductPO{}).
			Where("coupon_id = ? AND product_id = ?", coupon.ID, req.ProductID).
			Count(&allowedCount).Error; err != nil {
			return err
		}
		if allowedCount == 0 {
			return domain.ErrCouponNotApplicable
		}

		if coupon.PerUserLimit > 0 {
			var userCount int64
			if err := tx.Model(&CouponRedemptionPO{}).
				Where("coupon_id = ? AND user_id = ?", coupon.ID, req.UserID).
				Count(&userCount).Error; err != nil {
				return err
			}
			if userCount >= int64(coupon.PerUserLimit) {
				return domain.ErrCouponUserLimit
			}
		}

		discountAmount, finalAmount, err := coupon.CalculateDiscount(req.OriginalAmount)
		if err != nil {
			return err
		}

		update := tx.Model(&CouponPO{}).
			Where("id = ? AND enabled = ? AND (max_redemptions = ? OR used_count < max_redemptions)",
				coupon.ID, true, domain.UnlimitedRedemptions).
			Update("used_count", gorm.Expr("used_count + 1"))
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return domain.ErrCouponExhausted
		}

		redemptionPO := CouponRedemptionPO{
			ID:             req.RedemptionID,
			CouponID:       coupon.ID,
			Code:           coupon.Code,
			UserID:         req.UserID,
			OrderID:        req.OrderID,
			ProductID:      req.ProductID,
			OriginalAmount: req.OriginalAmount,
			DiscountAmount: discountAmount,
			FinalAmount:    finalAmount,
			RedeemedAt:     now,
		}
		if err := tx.Create(&redemptionPO).Error; err != nil {
			return err
		}
		redemption = redemptionToDomain(redemptionPO)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &redemption, nil
}

func (r *GormCouponRepo) allowedProducts(ctx context.Context, couponID string) ([]string, error) {
	var rows []CouponAllowedProductPO
	if err := r.db.WithContext(ctx).Where("coupon_id = ?", couponID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.ProductID
	}
	return out, nil
}

func couponToPO(c *domain.Coupon) CouponPO {
	return CouponPO{
		ID:             c.ID,
		Code:           c.Code,
		DiscountType:   c.DiscountType,
		DiscountValue:  c.DiscountValue,
		StartsAt:       c.StartsAt,
		ExpiresAt:      c.ExpiresAt,
		MaxRedemptions: c.MaxRedemptions,
		UsedCount:      c.UsedCount,
		PerUserLimit:   c.PerUserLimit,
		Enabled:        c.Enabled,
		Description:    c.Description,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}

func couponToDomain(po CouponPO) *domain.Coupon {
	return &domain.Coupon{
		ID:             po.ID,
		Code:           po.Code,
		DiscountType:   po.DiscountType,
		DiscountValue:  po.DiscountValue,
		StartsAt:       po.StartsAt,
		ExpiresAt:      po.ExpiresAt,
		MaxRedemptions: po.MaxRedemptions,
		UsedCount:      po.UsedCount,
		PerUserLimit:   po.PerUserLimit,
		Enabled:        po.Enabled,
		Description:    po.Description,
		CreatedAt:      po.CreatedAt,
		UpdatedAt:      po.UpdatedAt,
	}
}

func redemptionToDomain(po CouponRedemptionPO) domain.Redemption {
	return domain.Redemption{
		ID:             po.ID,
		CouponID:       po.CouponID,
		Code:           po.Code,
		UserID:         po.UserID,
		OrderID:        po.OrderID,
		ProductID:      po.ProductID,
		OriginalAmount: po.OriginalAmount,
		DiscountAmount: po.DiscountAmount,
		FinalAmount:    po.FinalAmount,
		RedeemedAt:     po.RedeemedAt,
	}
}

var _ app.CouponRepository = (*GormCouponRepo)(nil)
