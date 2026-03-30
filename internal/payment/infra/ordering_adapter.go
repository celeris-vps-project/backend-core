package infra

import (
	orderingApp "backend-core/internal/ordering/app"
	paymentApp "backend-core/internal/payment/app"
)

// OrderingAdapter implements paymentApp.OrderActivator by wrapping the
// ordering context's OrderAppService. It converts ordering domain types into
// the payment context's PayableOrder DTO so the payment context never imports
// ordering domain types directly.
type OrderingAdapter struct {
	svc *orderingApp.OrderAppService
}

// NewOrderingAdapter wraps an OrderAppService as an OrderActivator.
func NewOrderingAdapter(svc *orderingApp.OrderAppService) *OrderingAdapter {
	return &OrderingAdapter{svc: svc}
}

// ActivateOrder delegates to the ordering app service.
func (a *OrderingAdapter) ActivateOrder(orderID string) error {
	return a.svc.ActivateOrder(orderID)
}

// LinkInvoiceToOrder delegates to the ordering app service.
func (a *OrderingAdapter) LinkInvoiceToOrder(orderID, invoiceID string) error {
	return a.svc.LinkInvoiceToOrder(orderID, invoiceID)
}

// CancelOrder delegates to the ordering app service.
func (a *OrderingAdapter) CancelOrder(orderID, reason string) error {
	return a.svc.CancelOrder(orderID, reason)
}

func (a *OrderingAdapter) SuspendOrder(orderID string) error {
	return a.svc.SuspendOrder(orderID)
}

func (a *OrderingAdapter) UnsuspendOrder(orderID string) error {
	return a.svc.UnsuspendOrder(orderID)
}

func (a *OrderingAdapter) ReplaceInvoice(orderID, invoiceID string) error {
	return a.svc.ReplaceInvoice(orderID, invoiceID)
}

func (a *OrderingAdapter) ListOrders() ([]paymentApp.PayableOrder, error) {
	orders, err := a.svc.ListAll()
	if err != nil {
		return nil, err
	}
	result := make([]paymentApp.PayableOrder, len(orders))
	for i, order := range orders {
		cfg := order.VPSConfig()
		result[i] = paymentApp.PayableOrder{
			ID:           order.ID(),
			CustomerID:   order.CustomerID(),
			ProductID:    order.ProductID(),
			InvoiceID:    order.InvoiceID(),
			BillingCycle: order.BillingCycle(),
			Status:       order.Status(),
			Currency:     order.Currency(),
			PriceAmount:  order.PriceAmount(),
			Hostname:     cfg.Hostname(),
			Plan:         cfg.Plan(),
			Region:       cfg.Region(),
			OS:           cfg.OS(),
			CPU:          cfg.CPU(),
			MemoryMB:     cfg.MemoryMB(),
			DiskGB:       cfg.DiskGB(),
		}
	}
	return result, nil
}

// GetOrderForPayment retrieves an order and maps it to a PayableOrder DTO.
// This prevents the payment context from importing ordering/domain types.
func (a *OrderingAdapter) GetOrderForPayment(orderID string) (paymentApp.PayableOrder, error) {
	order, err := a.svc.GetOrder(orderID)
	if err != nil {
		return paymentApp.PayableOrder{}, err
	}
	cfg := order.VPSConfig()
	return paymentApp.PayableOrder{
		ID:           order.ID(),
		CustomerID:   order.CustomerID(),
		ProductID:    order.ProductID(),
		InvoiceID:    order.InvoiceID(),
		BillingCycle: order.BillingCycle(),
		Status:       order.Status(),
		Currency:     order.Currency(),
		PriceAmount:  order.PriceAmount(),
		Hostname:     cfg.Hostname(),
		Plan:         cfg.Plan(),
		Region:       cfg.Region(),
		OS:           cfg.OS(),
		CPU:          cfg.CPU(),
		MemoryMB:     cfg.MemoryMB(),
		DiskGB:       cfg.DiskGB(),
	}, nil
}
