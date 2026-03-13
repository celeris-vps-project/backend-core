package app

import (
	"backend-core/internal/billing/domain"
	"errors"
	"testing"
	"time"
)

type memoryInvoiceRepo struct {
	items map[string]*domain.Invoice
}

func newMemoryInvoiceRepo() *memoryInvoiceRepo {
	return &memoryInvoiceRepo{items: map[string]*domain.Invoice{}}
}

func (r *memoryInvoiceRepo) GetByID(id string) (*domain.Invoice, error) {
	invoice, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return invoice, nil
}

func (r *memoryInvoiceRepo) Save(invoice *domain.Invoice) error {
	r.items[invoice.ID()] = invoice
	return nil
}

func (r *memoryInvoiceRepo) ListByCustomerID(customerID string) ([]*domain.Invoice, error) {
	var result []*domain.Invoice
	for _, inv := range r.items {
		if inv.CustomerID() == customerID {
			result = append(result, inv)
		}
	}
	return result, nil
}

func (r *memoryInvoiceRepo) ExistsByID(id string) (bool, error) {
	_, ok := r.items[id]
	return ok, nil
}

type staticIDGen struct {
	id string
}

func (g staticIDGen) NewID() string { return g.id }

func TestInvoiceAppCreateAndIssue(t *testing.T) {
	repo := newMemoryInvoiceRepo()
	service := NewInvoiceAppService(repo, staticIDGen{id: "inv-123"}, nil)

	invoice, err := service.CreateDraft("cust-1", "USD", domain.OneTimeCycle(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invoice.ID() != "inv-123" {
		t.Fatalf("expected id inv-123, got %s", invoice.ID())
	}

	unitPrice, err := domain.NewMoney("USD", 800)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	item, err := domain.NewLineItem("item-1", "Domain", 1, unitPrice)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := service.AddLineItem(invoice.ID(), item); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	issuedAt := time.Now()
	if err := service.IssueInvoice(invoice.ID(), issuedAt, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, err := repo.GetByID(invoice.ID())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stored.Status() != domain.InvoiceStatusIssued {
		t.Fatalf("expected issued status, got %s", stored.Status())
	}
}

func TestInvoiceAppCreateMonthlyDraft(t *testing.T) {
	repo := newMemoryInvoiceRepo()
	service := NewInvoiceAppService(repo, staticIDGen{id: "inv-m1"}, nil)

	monthly, _ := domain.NewBillingCycle(domain.BillingCycleMonthly)
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	invoice, err := service.CreateDraft("cust-1", "USD", monthly, &start, &end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !invoice.IsRecurring() {
		t.Fatal("expected recurring invoice")
	}
	if invoice.BillingCycle().Type() != domain.BillingCycleMonthly {
		t.Fatalf("expected monthly, got %s", invoice.BillingCycle().Type())
	}
}

func TestGenerateRenewalInvoice(t *testing.T) {
	repo := newMemoryInvoiceRepo()
	service := NewInvoiceAppService(repo, staticIDGen{id: "inv-src"}, nil)

	monthly, _ := domain.NewBillingCycle(domain.BillingCycleMonthly)
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	// Create, add line item, issue, and pay the source invoice
	source, err := service.CreateDraft("cust-1", "USD", monthly, &start, &end)
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	unitPrice, _ := domain.NewMoney("USD", 1000)
	item, _ := domain.NewLineItem("item-1", "VPS Hosting", 1, unitPrice)
	if err := service.AddLineItem(source.ID(), item); err != nil {
		t.Fatalf("add line item: %v", err)
	}

	issuedAt := time.Now()
	if err := service.IssueInvoice(source.ID(), issuedAt, nil); err != nil {
		t.Fatalf("issue: %v", err)
	}

	payAmount, _ := domain.NewMoney("USD", 1000)
	if err := service.RecordPayment(source.ID(), payAmount, time.Now()); err != nil {
		t.Fatalf("pay: %v", err)
	}

	// Generate renewal
	renewal, err := service.GenerateRenewalInvoice(source.ID())
	if err != nil {
		t.Fatalf("generate renewal: %v", err)
	}

	// Verify renewal properties
	expectedID := "inv-src-renew-20260401"
	if renewal.ID() != expectedID {
		t.Fatalf("expected renewal id %s, got %s", expectedID, renewal.ID())
	}
	if renewal.Status() != domain.InvoiceStatusDraft {
		t.Fatalf("expected draft status, got %s", renewal.Status())
	}
	if !renewal.IsRecurring() {
		t.Fatal("renewal should be recurring")
	}
	if renewal.BillingCycle().Type() != domain.BillingCycleMonthly {
		t.Fatalf("expected monthly cycle, got %s", renewal.BillingCycle().Type())
	}

	// Verify period: next period should be Apr 1 → May 1
	wantStart := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if renewal.PeriodStart() == nil || !renewal.PeriodStart().Equal(wantStart) {
		t.Fatalf("period start: got %v, want %v", renewal.PeriodStart(), wantStart)
	}
	if renewal.PeriodEnd() == nil || !renewal.PeriodEnd().Equal(wantEnd) {
		t.Fatalf("period end: got %v, want %v", renewal.PeriodEnd(), wantEnd)
	}

	// Verify line items were copied
	if len(renewal.LineItems()) != 1 {
		t.Fatalf("expected 1 line item, got %d", len(renewal.LineItems()))
	}
	if renewal.Subtotal().Amount() != 1000 {
		t.Fatalf("expected subtotal 1000, got %d", renewal.Subtotal().Amount())
	}
}

func TestGenerateRenewalInvoice_Idempotent(t *testing.T) {
	repo := newMemoryInvoiceRepo()
	service := NewInvoiceAppService(repo, staticIDGen{id: "inv-idem"}, nil)

	monthly, _ := domain.NewBillingCycle(domain.BillingCycleMonthly)
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	source, _ := service.CreateDraft("cust-1", "USD", monthly, &start, &end)
	unitPrice, _ := domain.NewMoney("USD", 500)
	item, _ := domain.NewLineItem("item-1", "Hosting", 1, unitPrice)
	_ = service.AddLineItem(source.ID(), item)
	_ = service.IssueInvoice(source.ID(), time.Now(), nil)
	payAmount, _ := domain.NewMoney("USD", 500)
	_ = service.RecordPayment(source.ID(), payAmount, time.Now())

	// First renewal
	renewal1, err := service.GenerateRenewalInvoice(source.ID())
	if err != nil {
		t.Fatalf("first renewal: %v", err)
	}

	// Second renewal (idempotent — should return same invoice)
	renewal2, err := service.GenerateRenewalInvoice(source.ID())
	if err != nil {
		t.Fatalf("second renewal: %v", err)
	}

	if renewal1.ID() != renewal2.ID() {
		t.Fatalf("idempotency failed: %s != %s", renewal1.ID(), renewal2.ID())
	}
}

func TestGenerateRenewalInvoice_RejectsOneTime(t *testing.T) {
	repo := newMemoryInvoiceRepo()
	service := NewInvoiceAppService(repo, staticIDGen{id: "inv-ot"}, nil)

	source, _ := service.CreateDraft("cust-1", "USD", domain.OneTimeCycle(), nil, nil)
	unitPrice, _ := domain.NewMoney("USD", 500)
	item, _ := domain.NewLineItem("item-1", "Setup Fee", 1, unitPrice)
	_ = service.AddLineItem(source.ID(), item)
	_ = service.IssueInvoice(source.ID(), time.Now(), nil)
	payAmount, _ := domain.NewMoney("USD", 500)
	_ = service.RecordPayment(source.ID(), payAmount, time.Now())

	_, err := service.GenerateRenewalInvoice(source.ID())
	if err == nil {
		t.Fatal("expected error for one-time invoice renewal")
	}
}
