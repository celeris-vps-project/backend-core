package domain

import (
	"testing"
	"time"
)

func TestInvoiceIssueRequiresLineItems(t *testing.T) {
	invoice, err := NewDraftInvoice("inv-1", "cust-1", "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	issuedAt := time.Now()
	if err := invoice.Issue(issuedAt, nil); err == nil {
		t.Fatalf("expected error when issuing without line items")
	}
}

func TestInvoiceLifecycle(t *testing.T) {
	invoice, err := NewDraftInvoice("inv-2", "cust-2", "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	unitPrice, err := NewMoney("USD", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	item, err := NewLineItem("item-1", "Hosting", 2, unitPrice)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := invoice.AddLineItem(item); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tax, err := NewMoney("USD", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := invoice.SetTaxAmount(tax); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	issuedAt := time.Now()
	if err := invoice.Issue(issuedAt, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payAmount, err := NewMoney("USD", 1100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	paidAt := time.Now()
	if err := invoice.RecordPayment(payAmount, paidAt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if invoice.Status() != InvoiceStatusPaid {
		t.Fatalf("expected invoice to be paid, got %s", invoice.Status())
	}
	if invoice.Total().Amount() != 1100 {
		t.Fatalf("expected total 1100, got %d", invoice.Total().Amount())
	}
}
