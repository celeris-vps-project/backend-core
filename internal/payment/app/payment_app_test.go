package app_test

import (
	"backend-core/internal/payment/app"
	"backend-core/internal/payment/domain"
	"backend-core/pkg/apperr"
	"context"
	"errors"
	"testing"
)

// ── Minimal mock implementations for testing ───────────────────────────

type mockOrderActivator struct {
	order  app.PayableOrder
	getErr error
}

func (m *mockOrderActivator) ActivateOrder(orderID string) error { return nil }
func (m *mockOrderActivator) GetOrderForPayment(orderID string) (app.PayableOrder, error) {
	return m.order, m.getErr
}
func (m *mockOrderActivator) LinkInvoiceToOrder(orderID, invoiceID string) error { return nil }
func (m *mockOrderActivator) CancelOrder(orderID, reason string) error           { return nil }

type mockProductPurchaser struct{}

func (m *mockProductPurchaser) PurchaseProduct(ctx context.Context, productID, customerID, orderID, hostname, os string) (app.PurchasedProduct, error) {
	return app.PurchasedProduct{}, nil
}

type mockInstanceCreator struct{}

func (m *mockInstanceCreator) CreatePendingInstance(customerID, orderID, region, hostname, plan, os string, cpu, memoryMB, diskGB int) (string, error) {
	return "inst-1", nil
}

type mockInvoiceCreator struct{}

func (m *mockInvoiceCreator) CreateAndIssueInvoice(customerID, currency, billingCycle, description string, priceAmount int64) (string, error) {
	return "inv-1", nil
}
func (m *mockInvoiceCreator) RecordInvoicePayment(invoiceID string, amount int64, currency string) error {
	return nil
}
func (m *mockInvoiceCreator) VoidInvoice(invoiceID, reason string) error { return nil }
func (m *mockInvoiceCreator) GetInvoiceStatus(invoiceID string) (string, error) {
	return "issued", nil
}

type mockCryptoProvider struct {
	chargeResult *domain.ChargeResult
	chargeErr    error
}

func (m *mockCryptoProvider) CreateCharge(orderID, currency string, amountMinor int64) (*domain.ChargeResult, error) {
	return m.chargeResult, m.chargeErr
}
func (m *mockCryptoProvider) VerifyWebhook(rawBody []byte, signature string) (*domain.WebhookPayload, error) {
	return nil, nil
}
func (m *mockCryptoProvider) CreateCryptoCharge(orderID string, amountMinor int64, network domain.CryptoNetwork) (*domain.ChargeResult, error) {
	return m.chargeResult, m.chargeErr
}
func (m *mockCryptoProvider) GetNetworks() []domain.NetworkInfo {
	return domain.DefaultNetworkInfos()
}
func (m *mockCryptoProvider) GetChargeDetail(chargeID string) *domain.CryptoChargeDetail {
	return nil
}

// ── Tests ──────────────────────────────────────────────────────────────

func TestInitiatePayment_ValidationErrors(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{Status: "pending"}},
		&mockProductPurchaser{},
		&mockInstanceCreator{},
		&mockInvoiceCreator{},
		nil,
	)
	svc := app.NewPaymentAppService(nil, orch, nil)

	// Empty order ID
	_, err := svc.InitiatePayment(&app.InitiatePaymentRequest{})
	if err == nil {
		t.Fatal("expected error for empty order_id")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeInvalidParams {
		t.Fatalf("expected code %s, got %s", apperr.CodeInvalidParams, appErr.Code)
	}
}

func TestInitiatePayment_OrderNotFound(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{getErr: errors.New("not found")},
		&mockProductPurchaser{},
		nil, nil, nil,
	)
	svc := app.NewPaymentAppService(nil, orch, nil)

	_, err := svc.InitiatePayment(&app.InitiatePaymentRequest{OrderID: "order-999"})
	if err == nil {
		t.Fatal("expected error for missing order")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeOrderNotFound {
		t.Fatalf("expected code %s, got %s", apperr.CodeOrderNotFound, appErr.Code)
	}
}

func TestInitiatePayment_OrderNotPending(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{Status: "active"}},
		&mockProductPurchaser{},
		nil, nil, nil,
	)
	svc := app.NewPaymentAppService(nil, orch, nil)

	_, err := svc.InitiatePayment(&app.InitiatePaymentRequest{OrderID: "order-1"})
	if err == nil {
		t.Fatal("expected error for non-pending order")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeOrderNotPending {
		t.Fatalf("expected code %s, got %s", apperr.CodeOrderNotPending, appErr.Code)
	}
}

func TestInitiatePayment_CryptoSuccess(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{
			ID: "order-1", Status: "pending", PriceAmount: 2999,
		}},
		&mockProductPurchaser{},
		nil,
		&mockInvoiceCreator{},
		nil,
	)
	crypto := &mockCryptoProvider{
		chargeResult: &domain.ChargeResult{
			ChargeID: "crypto_arb_1",
			Status:   domain.ChargeStatusPending,
		},
	}
	svc := app.NewPaymentAppService(nil, orch, crypto)

	resp, err := svc.InitiatePayment(&app.InitiatePaymentRequest{
		OrderID: "order-1",
		Network: "arbitrum",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ChargeID != "crypto_arb_1" {
		t.Fatalf("expected charge ID crypto_arb_1, got %s", resp.ChargeID)
	}
	if resp.Status != domain.ChargeStatusPending {
		t.Fatalf("expected status pending, got %s", resp.Status)
	}
}

func TestInitiatePayment_UnsupportedNetwork(t *testing.T) {
	orch := app.NewPostPaymentOrchestrator(
		&mockOrderActivator{order: app.PayableOrder{
			ID: "order-1", Status: "pending", PriceAmount: 1000,
		}},
		&mockProductPurchaser{},
		nil, &mockInvoiceCreator{}, nil,
	)
	crypto := &mockCryptoProvider{}
	svc := app.NewPaymentAppService(nil, orch, crypto)

	_, err := svc.InitiatePayment(&app.InitiatePaymentRequest{
		OrderID: "order-1",
		Network: "invalid_network",
	})
	if err == nil {
		t.Fatal("expected error for unsupported network")
	}
	var appErr *apperr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T", err)
	}
	if appErr.Code != apperr.CodeNetworkUnsupported {
		t.Fatalf("expected code %s, got %s", apperr.CodeNetworkUnsupported, appErr.Code)
	}
}
