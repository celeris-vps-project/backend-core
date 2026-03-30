package app

import (
	"errors"
	"testing"
	"time"
)

type stubRenewalOrderManager struct {
	orders            []PayableOrder
	suspendedOrders   []string
	unsuspendedOrders []string
	replacedOrderID   string
	replacedInvoiceID string
}

func (m *stubRenewalOrderManager) ListOrders() ([]PayableOrder, error) { return m.orders, nil }
func (m *stubRenewalOrderManager) SuspendOrder(orderID string) error {
	m.suspendedOrders = append(m.suspendedOrders, orderID)
	return nil
}
func (m *stubRenewalOrderManager) UnsuspendOrder(orderID string) error {
	m.unsuspendedOrders = append(m.unsuspendedOrders, orderID)
	return nil
}
func (m *stubRenewalOrderManager) ReplaceInvoice(orderID, invoiceID string) error {
	m.replacedOrderID = orderID
	m.replacedInvoiceID = invoiceID
	return nil
}

type stubRenewalInvoiceManager struct {
	invoices map[string]RenewalInvoice
	paid     []string
	issued   []string
}

func (m *stubRenewalInvoiceManager) GetInvoice(invoiceID string) (RenewalInvoice, error) {
	invoice, ok := m.invoices[invoiceID]
	if !ok {
		return RenewalInvoice{}, errors.New("not found")
	}
	return invoice, nil
}

func (m *stubRenewalInvoiceManager) GenerateRenewalInvoice(sourceInvoiceID string) (RenewalInvoice, error) {
	invoice, ok := m.invoices[sourceInvoiceID+"-renew"]
	if !ok {
		return RenewalInvoice{}, errors.New("not found")
	}
	return invoice, nil
}

func (m *stubRenewalInvoiceManager) IssueInvoice(invoiceID string, issuedAt time.Time, dueAt *time.Time) error {
	m.issued = append(m.issued, invoiceID)
	invoice := m.invoices[invoiceID]
	invoice.Status = "issued"
	invoice.DueAt = dueAt
	m.invoices[invoiceID] = invoice
	return nil
}

func (m *stubRenewalInvoiceManager) RecordInvoicePayment(invoiceID string, amount int64, currency string) error {
	m.paid = append(m.paid, invoiceID)
	invoice := m.invoices[invoiceID]
	invoice.Status = "paid"
	m.invoices[invoiceID] = invoice
	return nil
}

type stubRenewalInstanceManager struct {
	instances          map[string]RenewalInstance
	suspendedInstances []string
	recoveredInstances []string
}

func (m *stubRenewalInstanceManager) GetByOrderID(orderID string) (RenewalInstance, error) {
	instance, ok := m.instances[orderID]
	if !ok {
		return RenewalInstance{}, errors.New("not found")
	}
	return instance, nil
}

func (m *stubRenewalInstanceManager) SuspendInstance(instanceID string) error {
	m.suspendedInstances = append(m.suspendedInstances, instanceID)
	return nil
}

func (m *stubRenewalInstanceManager) RecoverFromBillingSuspension(instanceID string) error {
	m.recoveredInstances = append(m.recoveredInstances, instanceID)
	return nil
}

func TestRenewalService_RunCycleGeneratesRenewalAheadOfExpiry(t *testing.T) {
	now := time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	orderMgr := &stubRenewalOrderManager{
		orders: []PayableOrder{{
			ID:           "ord-1",
			InvoiceID:    "inv-paid",
			BillingCycle: "monthly",
			Status:       "active",
		}},
	}
	invoiceMgr := &stubRenewalInvoiceManager{
		invoices: map[string]RenewalInvoice{
			"inv-paid":       {ID: "inv-paid", Status: "paid", PeriodEnd: &periodEnd},
			"inv-paid-renew": {ID: "inv-paid-renew", Status: "draft", PeriodEnd: ptrTime(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))},
		},
	}
	service := NewRenewalService(orderMgr, invoiceMgr, nil)

	if err := service.RunCycle(now, 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(invoiceMgr.issued) != 1 || invoiceMgr.issued[0] != "inv-paid-renew" {
		t.Fatalf("expected renewal invoice to be issued, got %v", invoiceMgr.issued)
	}
	if orderMgr.replacedOrderID != "ord-1" || orderMgr.replacedInvoiceID != "inv-paid-renew" {
		t.Fatalf("expected order invoice replacement, got order=%s invoice=%s", orderMgr.replacedOrderID, orderMgr.replacedInvoiceID)
	}
}

func TestRenewalService_RunCycleSuspendsExpiredActiveOrder(t *testing.T) {
	now := time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	orderMgr := &stubRenewalOrderManager{
		orders: []PayableOrder{{
			ID:           "ord-2",
			InvoiceID:    "inv-issued",
			BillingCycle: "monthly",
			Status:       "active",
		}},
	}
	invoiceMgr := &stubRenewalInvoiceManager{
		invoices: map[string]RenewalInvoice{
			"inv-issued": {ID: "inv-issued", Status: "issued", PeriodEnd: &periodEnd},
		},
	}
	instanceMgr := &stubRenewalInstanceManager{
		instances: map[string]RenewalInstance{
			"ord-2": {ID: "inst-2", Status: "running"},
		},
	}
	service := NewRenewalService(orderMgr, invoiceMgr, instanceMgr)

	if err := service.RunCycle(now, 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(orderMgr.suspendedOrders) != 1 || orderMgr.suspendedOrders[0] != "ord-2" {
		t.Fatalf("expected order suspension, got %v", orderMgr.suspendedOrders)
	}
	if len(instanceMgr.suspendedInstances) != 1 || instanceMgr.suspendedInstances[0] != "inst-2" {
		t.Fatalf("expected instance suspension, got %v", instanceMgr.suspendedInstances)
	}
}

func TestRenewalService_HandlePaidOrderRecoversSuspendedInstanceToStopped(t *testing.T) {
	periodEnd := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	orderMgr := &stubRenewalOrderManager{}
	invoiceMgr := &stubRenewalInvoiceManager{
		invoices: map[string]RenewalInvoice{
			"inv-issued": {ID: "inv-issued", Status: "issued", PeriodEnd: &periodEnd},
		},
	}
	instanceMgr := &stubRenewalInstanceManager{
		instances: map[string]RenewalInstance{
			"ord-3": {ID: "inst-3", Status: "suspended"},
		},
	}
	service := NewRenewalService(orderMgr, invoiceMgr, instanceMgr)

	err := service.HandlePaidOrder(PayableOrder{
		ID:           "ord-3",
		InvoiceID:    "inv-issued",
		BillingCycle: "monthly",
		Status:       "suspended",
		Currency:     "USD",
		PriceAmount:  1500,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(invoiceMgr.paid) != 1 || invoiceMgr.paid[0] != "inv-issued" {
		t.Fatalf("expected invoice payment recording, got %v", invoiceMgr.paid)
	}
	if len(orderMgr.unsuspendedOrders) != 1 || orderMgr.unsuspendedOrders[0] != "ord-3" {
		t.Fatalf("expected order unsuspend, got %v", orderMgr.unsuspendedOrders)
	}
	if len(instanceMgr.recoveredInstances) != 1 || instanceMgr.recoveredInstances[0] != "inst-3" {
		t.Fatalf("expected instance recovery to stopped, got %v", instanceMgr.recoveredInstances)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
