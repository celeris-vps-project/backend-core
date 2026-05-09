package app

import (
	"backend-core/internal/promotion/domain"
	"backend-core/pkg/apperr"
	"context"
	"errors"
	"strings"
	"time"
)

type IDGenerator interface {
	NewID() string
}

type CouponWithProducts struct {
	Coupon            *domain.Coupon
	AllowedProductIDs []string
}

type RedeemCouponRequest struct {
	RedemptionID   string
	Code           string
	UserID         string
	OrderID        string
	ProductID      string
	OriginalAmount int64
}

type CouponRepository interface {
	Create(ctx context.Context, coupon *domain.Coupon, allowedProductIDs []string) error
	GetByID(ctx context.Context, id string) (*CouponWithProducts, error)
	List(ctx context.Context) ([]CouponWithProducts, error)
	SetEnabled(ctx context.Context, id string, enabled bool) error
	FindRedemptionByOrder(ctx context.Context, orderID string) (*domain.Redemption, error)
	Redeem(ctx context.Context, req RedeemCouponRequest, now time.Time) (*domain.Redemption, error)
	GetByCodeWithProductID(ctx context.Context, code, productID string) (*domain.Coupon, error)
	CountUserCouponRedemptions(ctx context.Context, userID string, couponID string) (int64, error)
	ReleaseCouponForOrder(ctx context.Context, orderID string) error
	ActivateCodeAfterPayment(ctx context.Context, orderID string) error
}

type CouponAppService struct {
	repo CouponRepository
	ids  IDGenerator
	now  func() time.Time
}

func NewCouponAppService(repo CouponRepository, ids IDGenerator) *CouponAppService {
	return &CouponAppService{
		repo: repo,
		ids:  ids,
		now:  func() time.Time { return time.Now().UTC() },
	}
}

type CreateCouponRequest struct {
	Code              string
	DiscountType      string
	DiscountValue     int64
	StartsAt          *time.Time
	ExpiresAt         *time.Time
	MaxRedemptions    int
	PerUserLimit      int
	Description       string
	AllowedProductIDs []string
}

type ApplyCouponRequest struct {
	Code           string
	UserID         string
	OrderID        string
	ProductID      string
	OriginalAmount int64
}

type ApplyCouponResult struct {
	Applied        bool
	CouponID       string
	Code           string
	DiscountAmount int64
	FinalAmount    int64
}

type PreApplyCouponResult struct {
	Applied        bool
	Coupon         domain.Coupon
	DiscountAmount int64
	FinalAmount    int64
}

func (s *CouponAppService) CreateCoupon(ctx context.Context, req CreateCouponRequest) (*CouponWithProducts, error) {
	if s.ids == nil {
		return nil, apperr.ErrInternal("id generator is required")
	}
	allowed := normalizeIDs(req.AllowedProductIDs)
	if len(allowed) == 0 {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "allowed_product_ids is required")
	}
	perUserLimit := req.PerUserLimit
	if perUserLimit == 0 {
		perUserLimit = 1
	}
	coupon, err := domain.NewCoupon(
		s.ids.NewID(),
		req.Code,
		req.DiscountType,
		req.DiscountValue,
		req.StartsAt,
		req.ExpiresAt,
		req.MaxRedemptions,
		perUserLimit,
		req.Description,
		s.now(),
	)
	if err != nil {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, err.Error())
	}
	if err := s.repo.Create(ctx, coupon, allowed); err != nil {
		return nil, err
	}
	return &CouponWithProducts{Coupon: coupon, AllowedProductIDs: allowed}, nil
}

func (s *CouponAppService) GetCoupon(ctx context.Context, id string) (*CouponWithProducts, error) {
	coupon, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, mapCouponError(err)
	}
	return coupon, nil
}

func (s *CouponAppService) ListCoupons(ctx context.Context) ([]CouponWithProducts, error) {
	return s.repo.List(ctx)
}

func (s *CouponAppService) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if id == "" {
		return apperr.ErrBadRequest(apperr.CodeInvalidParams, "coupon id is required")
	}
	if err := s.repo.SetEnabled(ctx, id, enabled); err != nil {
		return mapCouponError(err)
	}
	return nil
}

func (s *CouponAppService) PreApplied(ctx context.Context, couponCode, userID, productID string, originalAmount int64) (*PreApplyCouponResult, error) {
	if userID == "" || productID == "" {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "order_id, user_id and product_id are required")
	}

	if originalAmount < 0 {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "original amount must be >= 0")
	}

	code := domain.NormalizeCode(couponCode)
	if code == "" {
		return &PreApplyCouponResult{Applied: false, FinalAmount: originalAmount}, nil
	}
	coupon, err := s.repo.GetByCodeWithProductID(ctx, couponCode, productID)
	if err != nil {
		return nil, err
	}
	cnt, err := s.repo.CountUserCouponRedemptions(ctx, userID, coupon.ID)
	if err != nil {
		return nil, err
	}
	if int(cnt) >= coupon.PerUserLimit {
		return nil, apperr.ErrConflict(apperr.CodeCouponUserLimited, "per user limit reached")
	}
	discountAmount, finalAmount, err := coupon.CalculateDiscount(originalAmount)
	if err != nil {
		return nil, err
	}

	return &PreApplyCouponResult{
		Applied:        true,
		Coupon:         *coupon,
		DiscountAmount: discountAmount,
		FinalAmount:    finalAmount,
	}, nil
}

func (s *CouponAppService) ApplyCoupon(ctx context.Context, req ApplyCouponRequest) (*ApplyCouponResult, error) {
	if req.OrderID == "" || req.UserID == "" || req.ProductID == "" {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "order_id, user_id and product_id are required")
	}
	if req.OriginalAmount < 0 {
		return nil, apperr.ErrBadRequest(apperr.CodeInvalidParams, "original amount must be >= 0")
	}

	if existing, err := s.repo.FindRedemptionByOrder(ctx, req.OrderID); err != nil {
		return nil, err
	} else if existing != nil {
		code := domain.NormalizeCode(req.Code)
		if code != "" && code != existing.Code {
			err := s.repo.ReleaseCouponForOrder(ctx, req.OrderID)
			if err != nil {
				return nil, err
			}
			return nil, apperr.ErrUnprocessable(apperr.CodeCouponInvalid, "order already redeemed another coupon")
		}
		return &ApplyCouponResult{
			Applied:        true,
			CouponID:       existing.CouponID,
			Code:           existing.Code,
			DiscountAmount: existing.DiscountAmount,
			FinalAmount:    existing.FinalAmount,
		}, nil
	}

	code := domain.NormalizeCode(req.Code)
	if code == "" {
		return &ApplyCouponResult{Applied: false, FinalAmount: req.OriginalAmount}, nil
	}
	redemption, err := s.repo.Redeem(ctx, RedeemCouponRequest{
		RedemptionID:   s.ids.NewID(),
		Code:           code,
		UserID:         req.UserID,
		OrderID:        req.OrderID,
		ProductID:      req.ProductID,
		OriginalAmount: req.OriginalAmount,
	}, s.now())
	if err != nil {
		return nil, mapCouponError(err)
	}
	return &ApplyCouponResult{
		Applied:        true,
		CouponID:       redemption.CouponID,
		Code:           redemption.Code,
		DiscountAmount: redemption.DiscountAmount,
		FinalAmount:    redemption.FinalAmount,
	}, nil
}

func (s *CouponAppService) ActivateCodeAfterPayment(ctx context.Context, orderID string) error {
	return s.repo.ActivateCodeAfterPayment(ctx, orderID)
}

func (s *CouponAppService) ReleaseCouponForOrder(ctx context.Context, orderID string) error {
	return s.repo.ReleaseCouponForOrder(ctx, orderID)
}

func mapCouponError(err error) error {
	switch {
	case errors.Is(err, domain.ErrCouponNotFound):
		return apperr.ErrNotFound(apperr.CodeCouponNotFound, err.Error())
	case errors.Is(err, domain.ErrCouponExhausted):
		return apperr.ErrUnprocessable(apperr.CodeCouponExhausted, err.Error())
	case errors.Is(err, domain.ErrCouponInactive),
		errors.Is(err, domain.ErrCouponExpired),
		errors.Is(err, domain.ErrCouponNotStarted),
		errors.Is(err, domain.ErrCouponNotApplicable),
		errors.Is(err, domain.ErrCouponUserLimit):
		return apperr.ErrUnprocessable(apperr.CodeCouponInvalid, err.Error())
	default:
		return err
	}
}

func normalizeIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
