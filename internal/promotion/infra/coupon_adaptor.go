package infra

import (
	paymentApp "backend-core/internal/payment/app"
	"backend-core/internal/promotion/app"
	"context"
	"fmt"
	"log"
)

var _ paymentApp.CouponReleaser = (*PromotionCouponAdapter)(nil)

type PromotionCouponAdapter struct {
	couponApp *app.CouponAppService
}

func NewPromotionCouponAdapter(couponApp *app.CouponAppService) *PromotionCouponAdapter {
	return &PromotionCouponAdapter{
		couponApp: couponApp,
	}
}

func (a *PromotionCouponAdapter) ReleaseCoupon(orderID string) error {
	if a == nil || a.couponApp == nil {
		return nil
	}

	if orderID == "" {
		return nil
	}

	err := a.couponApp.ReleaseCouponForOrder(context.Background(), orderID)
	if err != nil {
		log.Printf("[PromotionCouponAdapter] failed to release coupon: order=%s err=%v",
			orderID, err)

		return fmt.Errorf("release coupon via promotion failed: %w", err)
	}

	return nil
}
