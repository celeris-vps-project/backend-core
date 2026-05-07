package infra

import (
	paymentApp "backend-core/internal/payment/app"
	promotionApp "backend-core/internal/promotion/app"
	"context"
	"fmt"
	"log"
)

type PaymentCouponAdapter struct {
	svc *promotionApp.CouponAppService
}

func NewPaymentCouponAdapter(svc *promotionApp.CouponAppService) *PaymentCouponAdapter {
	return &PaymentCouponAdapter{svc: svc}
}

func (a *PaymentCouponAdapter) ApplyCoupon(ctx context.Context, req paymentApp.CouponApplicationRequest) (paymentApp.CouponApplicationResult, error) {
	result, err := a.svc.ApplyCoupon(ctx, promotionApp.ApplyCouponRequest{
		Code:           req.Code,
		UserID:         req.UserID,
		OrderID:        req.OrderID,
		ProductID:      req.ProductID,
		OriginalAmount: req.OriginalAmount,
	})
	if err != nil {
		return paymentApp.CouponApplicationResult{}, err
	}
	return paymentApp.CouponApplicationResult{
		Applied:        result.Applied,
		CouponID:       result.CouponID,
		Code:           result.Code,
		DiscountAmount: result.DiscountAmount,
		FinalAmount:    result.FinalAmount,
	}, nil
}

func (a *PaymentCouponAdapter) ReleaseCoupon(orderID string) error {
	if a == nil || a.svc == nil {
		return nil
	}

	if orderID == "" {
		return nil
	}

	err := a.svc.ReleaseCouponForOrder(context.Background(), orderID)
	if err != nil {
		log.Printf("[PromotionCouponAdapter] failed to release coupon: order=%s err=%v",
			orderID, err)

		return fmt.Errorf("release coupon via promotion failed: %w", err)
	}

	return nil
}

func (a *PaymentCouponAdapter) ActivateCodeAfterPayment(orderID string) error {
	if a == nil || a.svc == nil {
		return nil
	}

	if orderID == "" {
		return nil
	}

	err := a.svc.ActivateCodeAfterPayment(context.Background(), orderID)
	if err != nil {
		log.Printf("[PromotionCouponAdapter] failed to activate coupon: order=%s err=%v",
			orderID, err)

		return fmt.Errorf("activate coupon via promotion failed: %w", err)
	}

	return nil
}

var _ paymentApp.CouponApplier = (*PaymentCouponAdapter)(nil)
var _ paymentApp.CouponReleaser = (*PaymentCouponAdapter)(nil)
