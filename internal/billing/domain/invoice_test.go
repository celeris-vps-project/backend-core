package domain

import (
	"testing"
	"time"
)

func TestInvoiceIssueRequiresLineItems(t *testing.T) {
	invoice, err := NewDraftInvoice("inv-1", "cust-1", "USD", OneTimeCycle(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	issuedAt := time.Now()
	if err := invoice.Issue(issuedAt, nil); err == nil {
		t.Fatalf("expected error when issuing without line items")
	}
}

func TestInvoiceLifecycle(t *testing.T) {
	invoice, err := NewDraftInvoice("inv-2", "cust-2", "USD", OneTimeCycle(), nil, nil)
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

func TestMonthlyInvoice(t *testing.T) {
	monthly, _ := NewBillingCycle(BillingCycleMonthly)
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	invoice, err := NewDraftInvoice("inv-m1", "cust-1", "USD", monthly, &start, &end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !invoice.IsRecurring() {
		t.Fatal("monthly invoice should be recurring")
	}
	if invoice.BillingCycle().Type() != BillingCycleMonthly {
		t.Fatalf("expected monthly cycle, got %s", invoice.BillingCycle().Type())
	}
	if invoice.PeriodStart() == nil || !invoice.PeriodStart().Equal(start) {
		t.Fatalf("period start mismatch")
	}
	if invoice.PeriodEnd() == nil || !invoice.PeriodEnd().Equal(end) {
		t.Fatalf("period end mismatch")
	}
}

func TestYearlyInvoice(t *testing.T) {
	yearly, _ := NewBillingCycle(BillingCycleYearly)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	invoice, err := NewDraftInvoice("inv-y1", "cust-1", "USD", yearly, &start, &end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !invoice.IsRecurring() {
		t.Fatal("yearly invoice should be recurring")
	}
	if invoice.BillingCycle().Type() != BillingCycleYearly {
		t.Fatalf("expected yearly cycle, got %s", invoice.BillingCycle().Type())
	}
}

func TestRecurringInvoiceRequiresPeriod(t *testing.T) {
	monthly, _ := NewBillingCycle(BillingCycleMonthly)

	// Missing period
	_, err := NewDraftInvoice("inv-err", "cust-1", "USD", monthly, nil, nil)
	if err == nil {
		t.Fatal("expected error for recurring invoice without period")
	}

	// Period end before start
	start := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	_, err = NewDraftInvoice("inv-err2", "cust-1", "USD", monthly, &start, &end)
	if err == nil {
		t.Fatal("expected error for period end before start")
	}
}

func TestOneTimeInvoiceNoPeriodRequired(t *testing.T) {
	invoice, err := NewDraftInvoice("inv-ot", "cust-1", "USD", OneTimeCycle(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invoice.IsRecurring() {
		t.Fatal("one_time invoice should not be recurring")
	}
}
