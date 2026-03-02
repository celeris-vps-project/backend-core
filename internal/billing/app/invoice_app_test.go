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

type staticIDGen struct {
	id string
}

func (g staticIDGen) NewID() string { return g.id }

func TestInvoiceAppCreateAndIssue(t *testing.T) {
	repo := newMemoryInvoiceRepo()
	service := NewInvoiceAppService(repo, staticIDGen{id: "inv-123"}, nil)

	invoice, err := service.CreateDraft("cust-1", "USD")
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
