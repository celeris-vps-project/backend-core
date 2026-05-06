package infra

import (
	catalogApp "backend-core/internal/catalog/app"
	paymentApp "backend-core/internal/payment/app"
	"context"
)

// CatalogAdapter implements paymentApp.ProductPurchaser by wrapping the
// catalog context's ProductAppService.
type CatalogAdapter struct {
	svc *catalogApp.ProductAppService
}

func NewCatalogAdapter(svc *catalogApp.ProductAppService) *CatalogAdapter {
	return &CatalogAdapter{svc: svc}
}

func (a *CatalogAdapter) PurchaseProduct(ctx context.Context, productID, customerID, orderID, instanceID, initialPassword, hostname, os, networkMode string) (paymentApp.PurchasedProduct, error) {
	product, err := a.svc.PurchaseProduct(ctx, productID, customerID, orderID, instanceID, initialPassword, hostname, os, networkMode)
	if err != nil {
		return paymentApp.PurchasedProduct{}, err
	}
	return paymentApp.PurchasedProduct{
		Location:    product.Location(),
		Slug:        product.Slug(),
		CPU:         product.CPU(),
		MemoryMB:    product.MemoryMB(),
		DiskGB:      product.DiskGB(),
		BandwidthGB: product.BandwidthGB(),
	}, nil
}

func (a *CatalogAdapter) ReserveProduct(ctx context.Context, productID string) error {
	return a.svc.ReserveProduct(ctx, productID)
}

func (a *CatalogAdapter) ReleaseProduct(ctx context.Context, productID string) error {
	return a.svc.ReleaseProduct(ctx, productID)
}
