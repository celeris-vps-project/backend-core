package infra

import (
	paymentApp "backend-core/internal/payment/app"
	promotionApp "backend-core/internal/promotion/app"
	"context"
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

var _ paymentApp.CouponApplier = (*PaymentCouponAdapter)(nil)
