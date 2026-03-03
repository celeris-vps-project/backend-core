package domain

import "testing"

func TestNewProduct(t *testing.T) {
	p, err := NewProduct("p-1", "VPS Starter", "vps-starter", 1, 1024, 20, 1000, 499, "USD", BillingMonthly)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Slug() != "vps-starter" {
		t.Fatalf("expected slug vps-starter, got %s", p.Slug())
	}
	if !p.Enabled() {
		t.Fatal("expected enabled by default")
	}
}

func TestProduct_DisableEnable(t *testing.T) {
	p, _ := NewProduct("p-2", "VPS Pro", "vps-pro", 4, 8192, 100, 5000, 1999, "USD", BillingMonthly)
	p.Disable()
	if p.Enabled() {
		t.Fatal("expected disabled")
	}
	p.Enable()
	if !p.Enabled() {
		t.Fatal("expected enabled")
	}
}

func TestProduct_SetPrice(t *testing.T) {
	p, _ := NewProduct("p-3", "VPS Basic", "vps-basic", 2, 2048, 40, 2000, 999, "EUR", BillingMonthly)
	if err := p.SetPrice(1299, "EUR"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.PriceAmount() != 1299 {
		t.Fatalf("expected 1299, got %d", p.PriceAmount())
	}
	if err := p.SetPrice(0, "EUR"); err == nil {
		t.Fatal("expected error for zero price")
	}
}
