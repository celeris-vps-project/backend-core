package http

import (
	"backend-core/internal/promotion/app"
	"backend-core/pkg/apperr"
	"context"
	"time"

	hz_app "github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type CouponHandler struct {
	svc *app.CouponAppService
}

func NewCouponHandler(svc *app.CouponAppService) *CouponHandler {
	return &CouponHandler{svc: svc}
}

type CreateCouponRequest struct {
	Code              string   `json:"code" vd:"len($)>0"`
	DiscountType      string   `json:"discount_type" vd:"len($)>0"`
	DiscountValue     int64    `json:"discount_value"`
	StartsAt          *string  `json:"starts_at"`
	ExpiresAt         *string  `json:"expires_at"`
	MaxRedemptions    int      `json:"max_redemptions"`
	PerUserLimit      int      `json:"per_user_limit"`
	Description       string   `json:"description"`
	AllowedProductIDs []string `json:"allowed_product_ids"`
}

type CouponResponse struct {
	ID                string    `json:"id"`
	Code              string    `json:"code"`
	DiscountType      string    `json:"discount_type"`
	DiscountValue     int64     `json:"discount_value"`
	StartsAt          *string   `json:"starts_at,omitempty"`
	ExpiresAt         *string   `json:"expires_at,omitempty"`
	MaxRedemptions    int       `json:"max_redemptions"`
	UsedCount         int       `json:"used_count"`
	PerUserLimit      int       `json:"per_user_limit"`
	Enabled           bool      `json:"enabled"`
	Description       string    `json:"description"`
	AllowedProductIDs []string  `json:"allowed_product_ids"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (h *CouponHandler) Create(ctx context.Context, c *hz_app.RequestContext) {
	var req CreateCouponRequest
	if err := c.BindAndValidate(&req); err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	startsAt, err := parseOptionalTime(req.StartsAt, "starts_at")
	if err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}
	expiresAt, err := parseOptionalTime(req.ExpiresAt, "expires_at")
	if err != nil {
		c.JSON(consts.StatusBadRequest, apperr.Resp(apperr.CodeInvalidParams, err.Error()))
		return
	}

	coupon, err := h.svc.CreateCoupon(ctx, app.CreateCouponRequest{
		Code:              req.Code,
		DiscountType:      req.DiscountType,
		DiscountValue:     req.DiscountValue,
		StartsAt:          startsAt,
		ExpiresAt:         expiresAt,
		MaxRedemptions:    req.MaxRedemptions,
		PerUserLimit:      req.PerUserLimit,
		Description:       req.Description,
		AllowedProductIDs: req.AllowedProductIDs,
	})
	if err != nil {
		apperr.HandleErr(c, err)
		return
	}
	c.JSON(consts.StatusCreated, utils.H{"data": toCouponResponse(*coupon)})
}

func (h *CouponHandler) List(ctx context.Context, c *hz_app.RequestContext) {
	coupons, err := h.svc.ListCoupons(ctx)
	if err != nil {
		c.JSON(consts.StatusInternalServerError, apperr.Resp(apperr.CodeInternalError, err.Error()))
		return
	}
	resp := make([]CouponResponse, len(coupons))
	for i, coupon := range coupons {
		resp[i] = toCouponResponse(coupon)
	}
	c.JSON(consts.StatusOK, utils.H{"data": resp})
}

func (h *CouponHandler) GetByID(ctx context.Context, c *hz_app.RequestContext) {
	coupon, err := h.svc.GetCoupon(ctx, c.Param("id"))
	if err != nil {
		apperr.HandleErr(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"data": toCouponResponse(*coupon)})
}

func (h *CouponHandler) Enable(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.SetEnabled(ctx, c.Param("id"), true); err != nil {
		apperr.HandleErr(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "coupon enabled"})
}

func (h *CouponHandler) Disable(ctx context.Context, c *hz_app.RequestContext) {
	if err := h.svc.SetEnabled(ctx, c.Param("id"), false); err != nil {
		apperr.HandleErr(c, err)
		return
	}
	c.JSON(consts.StatusOK, utils.H{"message": "coupon disabled"})
}

func toCouponResponse(item app.CouponWithProducts) CouponResponse {
	coupon := item.Coupon
	resp := CouponResponse{
		ID:                coupon.ID,
		Code:              coupon.Code,
		DiscountType:      coupon.DiscountType,
		DiscountValue:     coupon.DiscountValue,
		MaxRedemptions:    coupon.MaxRedemptions,
		UsedCount:         coupon.UsedCount,
		PerUserLimit:      coupon.PerUserLimit,
		Enabled:           coupon.Enabled,
		Description:       coupon.Description,
		AllowedProductIDs: item.AllowedProductIDs,
		CreatedAt:         coupon.CreatedAt,
		UpdatedAt:         coupon.UpdatedAt,
	}
	if coupon.StartsAt != nil {
		s := coupon.StartsAt.Format(time.RFC3339)
		resp.StartsAt = &s
	}
	if coupon.ExpiresAt != nil {
		s := coupon.ExpiresAt.Format(time.RFC3339)
		resp.ExpiresAt = &s
	}
	return resp
}

func parseOptionalTime(raw *string, field string) (*time.Time, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, &parseTimeError{field: field}
	}
	return &parsed, nil
}

type parseTimeError struct {
	field string
}

func (e *parseTimeError) Error() string {
	return e.field + " must be RFC3339"
}
