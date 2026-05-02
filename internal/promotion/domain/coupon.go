package domain

import (
	"errors"
	"strings"
	"time"
)

const (
	DiscountTypeFree    = "free"
	DiscountTypeFixed   = "fixed"
	DiscountTypePercent = "percent"

	UnlimitedRedemptions = -1
)

var (
	ErrCouponNotFound      = errors.New("coupon not found")
	ErrCouponInactive      = errors.New("coupon is inactive")
	ErrCouponExpired       = errors.New("coupon is expired")
	ErrCouponNotStarted    = errors.New("coupon is not active yet")
	ErrCouponExhausted     = errors.New("coupon redemption limit reached")
	ErrCouponNotApplicable = errors.New("coupon is not applicable to this product")
	ErrCouponUserLimit     = errors.New("coupon user redemption limit reached")
)

// Coupon is a small promotion aggregate for activation-code style discounts.
type Coupon struct {
	ID             string
	Code           string
	DiscountType   string
	DiscountValue  int64
	StartsAt       *time.Time
	ExpiresAt      *time.Time
	MaxRedemptions int
	UsedCount      int
	PerUserLimit   int
	Enabled        bool
	Description    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Redemption struct {
	ID             string
	CouponID       string
	Code           string
	UserID         string
	OrderID        string
	ProductID      string
	OriginalAmount int64
	DiscountAmount int64
	FinalAmount    int64
	RedeemedAt     time.Time
}

func NormalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func NewCoupon(
	id, code, discountType string,
	discountValue int64,
	startsAt, expiresAt *time.Time,
	maxRedemptions, perUserLimit int,
	description string,
	now time.Time,
) (*Coupon, error) {
	c := &Coupon{
		ID:             id,
		Code:           NormalizeCode(code),
		DiscountType:   strings.ToLower(strings.TrimSpace(discountType)),
		DiscountValue:  discountValue,
		StartsAt:       copyTime(startsAt),
		ExpiresAt:      copyTime(expiresAt),
		MaxRedemptions: maxRedemptions,
		UsedCount:      0,
		PerUserLimit:   perUserLimit,
		Enabled:        true,
		Description:    strings.TrimSpace(description),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Coupon) Validate() error {
	if c.ID == "" {
		return errors.New("domain_error: coupon id is required")
	}
	if c.Code == "" {
		return errors.New("domain_error: coupon code is required")
	}
	switch c.DiscountType {
	case DiscountTypeFree:
		if c.DiscountValue < 0 {
			return errors.New("domain_error: free coupon discount value must be >= 0")
		}
	case DiscountTypeFixed:
		if c.DiscountValue <= 0 {
			return errors.New("domain_error: fixed coupon discount value must be > 0")
		}
	case DiscountTypePercent:
		if c.DiscountValue <= 0 || c.DiscountValue > 100 {
			return errors.New("domain_error: percent coupon discount value must be 1..100")
		}
	default:
		return errors.New("domain_error: unsupported coupon discount type")
	}
	if c.MaxRedemptions != UnlimitedRedemptions && c.MaxRedemptions <= 0 {
		return errors.New("domain_error: max redemptions must be > 0 or -1")
	}
	if c.PerUserLimit < 0 {
		return errors.New("domain_error: per user limit must be >= 0")
	}
	if c.StartsAt != nil && c.ExpiresAt != nil && !c.ExpiresAt.After(*c.StartsAt) {
		return errors.New("domain_error: expires_at must be after starts_at")
	}
	return nil
}

func (c *Coupon) ValidateRedeemable(now time.Time) error {
	if !c.Enabled {
		return ErrCouponInactive
	}
	if c.StartsAt != nil && now.Before(*c.StartsAt) {
		return ErrCouponNotStarted
	}
	if c.ExpiresAt != nil && !now.Before(*c.ExpiresAt) {
		return ErrCouponExpired
	}
	if c.MaxRedemptions != UnlimitedRedemptions && c.UsedCount >= c.MaxRedemptions {
		return ErrCouponExhausted
	}
	return nil
}

func (c *Coupon) CalculateDiscount(originalAmount int64) (discountAmount, finalAmount int64, err error) {
	if originalAmount < 0 {
		return 0, 0, errors.New("domain_error: original amount must be >= 0")
	}
	switch c.DiscountType {
	case DiscountTypeFree:
		discountAmount = originalAmount
	case DiscountTypeFixed:
		discountAmount = c.DiscountValue
	case DiscountTypePercent:
		discountAmount = originalAmount * c.DiscountValue / 100
	default:
		return 0, 0, errors.New("domain_error: unsupported coupon discount type")
	}
	if discountAmount > originalAmount {
		discountAmount = originalAmount
	}
	finalAmount = originalAmount - discountAmount
	return discountAmount, finalAmount, nil
}

func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
