package infra

import (
	catalogApp "backend-core/internal/catalog/app"
	paymentApp "backend-core/internal/payment/app"
)

// CatalogAdapter implements paymentApp.ProductPurchaser by wrapping the
// catalog context's ProductAppService. It converts catalog domain types into
// the payment context's PurchasedProduct DTO so the payment context never
// imports catalog domain types directly.
type CatalogAdapter struct {
	svc *catalogApp.ProductAppService
}

// NewCatalogAdapter wraps a ProductAppService as a ProductPurchaser.
func NewCatalogAdapter(svc *catalogApp.ProductAppService) *CatalogAdapter {
	return &CatalogAdapter{svc: svc}
}

// PurchaseProduct delegates to the catalog app service and maps the result
// to a PurchasedProduct DTO.
func (a *CatalogAdapter) PurchaseProduct(productID, customerID, orderID, hostname, os string) (paymentApp.PurchasedProduct, error) {
	product, err := a.svc.PurchaseProduct(productID, customerID, orderID, hostname, os)
	if err != nil {
		return paymentApp.PurchasedProduct{}, err
	}
	return paymentApp.PurchasedProduct{
		Location: product.Location(),
		Slug:     product.Slug(),
		CPU:      product.CPU(),
		MemoryMB: product.MemoryMB(),
		DiskGB:   product.DiskGB(),
	}, nil
}
