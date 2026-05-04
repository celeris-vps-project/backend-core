package app_test

import (
	"backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type trackingOrderManager struct {
	order         app.PayableOrder
	getErr        error
	cancelCalls   int
	cancelReasons []string
}

func (m *trackingOrderManager) ActivateOrder(orderID string) error {
	m.order.Status = "active"
	return nil
}

func (m *trackingOrderManager) GetOrderForPayment(orderID string) (app.PayableOrder, error) {
	return m.order, m.getErr
}

func (m *trackingOrderManager) LinkInvoiceToOrder(orderID, invoiceID string) error {
	m.order.InvoiceID = invoiceID
	return nil
}

func (m *trackingOrderManager) CancelOrder(orderID, reason string) error {
	m.cancelCalls++
	m.cancelReasons = append(m.cancelReasons, reason)
	m.order.Status = "cancelled"
	return nil
}

func (m *trackingOrderManager) ListOrders() ([]app.PayableOrder, error) {
	return []app.PayableOrder{m.order}, nil
}

func (m *trackingOrderManager) SuspendOrder(orderID string) error {
	m.order.Status = "suspended"
	return nil
}

func (m *trackingOrderManager) UnsuspendOrder(orderID string) error {
	m.order.Status = "active"
	return nil
}

func (m *trackingOrderManager) ReplaceInvoice(orderID, invoiceID string) error {
	m.order.InvoiceID = invoiceID
	return nil
}

type trackingInvoiceManager struct {
	statuses    map[string]string
	voidReasons map[string]string
	nextID      int
}

func newTrackingInvoiceManager(initial map[string]string) *trackingInvoiceManager {
	statuses := make(map[string]string, len(initial))
	for k, v := range initial {
		statuses[k] = v
	}
	return &trackingInvoiceManager{
		statuses:    statuses,
		voidReasons: map[string]string{},
	}
}

func (m *trackingInvoiceManager) CreateAndIssueInvoice(customerID, currency, billingCycle, description string, priceAmount int64) (string, error) {
	m.nextID++
	invoiceID := fmt.Sprintf("inv-%d", m.nextID)
	m.statuses[invoiceID] = "issued"
	return invoiceID, nil
}

func (m *trackingInvoiceManager) RecordInvoicePayment(invoiceID string, amount int64, currency string) error {
	m.statuses[invoiceID] = "paid"
	return nil
}

func (m *trackingInvoiceManager) VoidInvoice(invoiceID, reason string) error {
	if _, ok := m.statuses[invoiceID]; !ok {
		return fmt.Errorf("invoice %s not found", invoiceID)
	}
	m.statuses[invoiceID] = "void"
	m.voidReasons[invoiceID] = reason
	return nil
}

func (m *trackingInvoiceManager) GetInvoiceStatus(invoiceID string) (string, error) {
	status, ok := m.statuses[invoiceID]
	if !ok {
		return "", fmt.Errorf("invoice %s not found", invoiceID)
	}
	return status, nil
}

func (m *trackingInvoiceManager) GetInvoiceForPayment(invoiceID string) (app.PayableInvoice, error) {
	status, ok := m.statuses[invoiceID]
	if !ok {
		return app.PayableInvoice{}, fmt.Errorf("invoice %s not found", invoiceID)
	}
	return app.PayableInvoice{
		ID:       invoiceID,
		Status:   status,
		Currency: "USD",
		Total:    2999,
	}, nil
}

func (m *trackingInvoiceManager) GetInvoice(invoiceID string) (app.RenewalInvoice, error) {
	status, ok := m.statuses[invoiceID]
	if !ok {
		return app.RenewalInvoice{}, fmt.Errorf("invoice %s not found", invoiceID)
	}
	return app.RenewalInvoice{
		ID:        invoiceID,
		Status:    status,
		PeriodEnd: ptrTime(time.Now().Add(24 * time.Hour)),
		DueAt:     ptrTime(time.Now().Add(24 * time.Hour)),
	}, nil
}

func (m *trackingInvoiceManager) GenerateRenewalInvoice(sourceInvoiceID string) (app.RenewalInvoice, error) {
	m.nextID++
	invoiceID := fmt.Sprintf("renew-%d", m.nextID)
	m.statuses[invoiceID] = "draft"
	return app.RenewalInvoice{ID: invoiceID, Status: "draft"}, nil
}

func (m *trackingInvoiceManager) IssueInvoice(invoiceID string, issuedAt time.Time, dueAt *time.Time) error {
	if _, ok := m.statuses[invoiceID]; !ok {
		return fmt.Errorf("invoice %s not found", invoiceID)
	}
	m.statuses[invoiceID] = "issued"
	return nil
}

type trackingProductManager struct {
	reserveCalls int
	releaseCalls int
	released     []string
}

func (m *trackingProductManager) PurchaseProduct(ctx context.Context, productID, customerID, orderID, instanceID, initialPassword, hostname, os, networkMode string) (app.PurchasedProduct, error) {
	return app.PurchasedProduct{}, nil
}

func (m *trackingProductManager) ReserveProduct(ctx context.Context, productID string) error {
	m.reserveCalls++
	return nil
}

func (m *trackingProductManager) ReleaseProduct(ctx context.Context, productID string) error {
	m.releaseCalls++
	m.released = append(m.released, productID)
	return nil
}

func ptrTime(v time.Time) *time.Time {
	return &v
}

func TestInitiatePayment_DoesNotVoidRenewalInvoiceOnChargeCreationFailure(t *testing.T) {
	orderMgr := &trackingOrderManager{
		order: app.PayableOrder{
			ID:           "order-1",
			Status:       "active",
			BillingCycle: "monthly",
			InvoiceID:    "inv-renew",
			PriceAmount:  2999,
			Currency:     "USD",
		},
	}
	invoiceMgr := newTrackingInvoiceManager(map[string]string{
		"inv-renew": "issued",
	})
	renewalSvc := app.NewRenewalService(orderMgr, invoiceMgr, nil)
	orch := app.NewPostPaymentOrchestrator(orderMgr, &mockProductPurchaser{}, nil, invoiceMgr, nil)
	crypto := &mockCryptoProvider{chargeErr: errors.New("gateway down")}
	svc := app.NewPaymentAppService(nil, orch, crypto)
	svc.SetRenewalService(renewalSvc)

	_, err := svc.InitiatePayment(context.Background(), &app.InitiatePaymentRequest{
		OrderID: "order-1",
		Network: "arbitrum",
	})
	if err == nil {
		t.Fatal("expected charge creation error")
	}
	if got := invoiceMgr.statuses["inv-renew"]; got != "issued" {
		t.Fatalf("expected renewal invoice to stay issued, got %s", got)
	}
	if len(invoiceMgr.voidReasons) != 0 {
		t.Fatalf("expected no renewal invoice to be voided, got %#v", invoiceMgr.voidReasons)
	}
}

func TestHandleWebhookPayload_FailedPendingPaymentVoidsCurrentInvoice(t *testing.T) {
	products := &trackingProductManager{}
	orderMgr := &trackingOrderManager{
		order: app.PayableOrder{
			ID:        "order-1",
			Status:    "pending",
			ProductID: "prod-1",
			InvoiceID: "inv-1",
		},
	}
	invoiceMgr := newTrackingInvoiceManager(map[string]string{
		"inv-1": "issued",
	})
	orch := app.NewPostPaymentOrchestrator(orderMgr, products, nil, invoiceMgr, nil)
	svc := app.NewPaymentAppService(nil, orch, nil)

	svc.HandleWebhookPayload(&domain.WebhookPayload{
		OrderID: "order-1",
		Status:  domain.ChargeStatusFailed,
	})

	if got := invoiceMgr.statuses["inv-1"]; got != "void" {
		t.Fatalf("expected pending invoice to be voided, got %s", got)
	}
	if orderMgr.cancelCalls != 0 {
		t.Fatalf("expected failed webhook to avoid immediate order cancellation, got %d cancels", orderMgr.cancelCalls)
	}
	if products.releaseCalls != 1 || products.released[0] != "prod-1" {
		t.Fatalf("expected reserved product to be released once, got calls=%d released=%v", products.releaseCalls, products.released)
	}
}

func TestHandleWebhookPayload_FailedRenewalPaymentKeepsInvoice(t *testing.T) {
	orderMgr := &trackingOrderManager{
		order: app.PayableOrder{
			ID:           "order-1",
			Status:       "active",
			BillingCycle: "monthly",
			InvoiceID:    "inv-renew",
		},
	}
	invoiceMgr := newTrackingInvoiceManager(map[string]string{
		"inv-renew": "issued",
	})
	orch := app.NewPostPaymentOrchestrator(orderMgr, &mockProductPurchaser{}, nil, invoiceMgr, nil)
	svc := app.NewPaymentAppService(nil, orch, nil)

	svc.HandleWebhookPayload(&domain.WebhookPayload{
		OrderID: "order-1",
		Status:  domain.ChargeStatusFailed,
	})

	if got := invoiceMgr.statuses["inv-renew"]; got != "issued" {
		t.Fatalf("expected renewal invoice to stay issued, got %s", got)
	}
	if len(invoiceMgr.voidReasons) != 0 {
		t.Fatalf("expected failed renewal webhook to keep invoice untouched, got %#v", invoiceMgr.voidReasons)
	}
}

func TestHandleInvoiceTimeout_CancelsCurrentPendingInvoice(t *testing.T) {
	products := &trackingProductManager{}
	orderMgr := &trackingOrderManager{
		order: app.PayableOrder{
			ID:        "order-1",
			Status:    "pending",
			ProductID: "prod-1",
			InvoiceID: "inv-1",
		},
	}
	invoiceMgr := newTrackingInvoiceManager(map[string]string{
		"inv-1": "issued",
	})
	orch := app.NewPostPaymentOrchestrator(orderMgr, products, nil, invoiceMgr, nil)

	orch.HandleInvoiceTimeout("inv-1", "order-1")

	if got := invoiceMgr.statuses["inv-1"]; got != "void" {
		t.Fatalf("expected timed-out invoice to be voided, got %s", got)
	}
	if orderMgr.cancelCalls != 1 {
		t.Fatalf("expected pending order to be cancelled once, got %d", orderMgr.cancelCalls)
	}
	if products.releaseCalls != 1 || products.released[0] != "prod-1" {
		t.Fatalf("expected reserved product to be released once, got calls=%d released=%v", products.releaseCalls, products.released)
	}
}

func TestHandleInvoiceTimeout_StaleInvoiceDoesNotCancelOrder(t *testing.T) {
	orderMgr := &trackingOrderManager{
		order: app.PayableOrder{
			ID:        "order-1",
			Status:    "pending",
			InvoiceID: "inv-current",
		},
	}
	invoiceMgr := newTrackingInvoiceManager(map[string]string{
		"inv-old": "issued",
	})
	orch := app.NewPostPaymentOrchestrator(orderMgr, &mockProductPurchaser{}, nil, invoiceMgr, nil)

	orch.HandleInvoiceTimeout("inv-old", "order-1")

	if got := invoiceMgr.statuses["inv-old"]; got != "void" {
		t.Fatalf("expected stale invoice to be voided, got %s", got)
	}
	if orderMgr.cancelCalls != 0 {
		t.Fatalf("expected stale invoice timeout to skip order cancellation, got %d", orderMgr.cancelCalls)
	}
}

func TestHandleInvoiceTimeout_SkipsActiveCurrentInvoice(t *testing.T) {
	orderMgr := &trackingOrderManager{
		order: app.PayableOrder{
			ID:        "order-1",
			Status:    "active",
			InvoiceID: "inv-renew",
		},
	}
	invoiceMgr := newTrackingInvoiceManager(map[string]string{
		"inv-renew": "issued",
	})
	orch := app.NewPostPaymentOrchestrator(orderMgr, &mockProductPurchaser{}, nil, invoiceMgr, nil)

	orch.HandleInvoiceTimeout("inv-renew", "order-1")

	if got := invoiceMgr.statuses["inv-renew"]; got != "issued" {
		t.Fatalf("expected active order invoice to stay issued, got %s", got)
	}
	if orderMgr.cancelCalls != 0 {
		t.Fatalf("expected active order timeout to skip cancellation, got %d", orderMgr.cancelCalls)
	}
}
