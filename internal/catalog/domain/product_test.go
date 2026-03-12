package domain

import "testing"

func TestNewProduct(t *testing.T) {
	p, err := NewProduct("p-1", "VPS Starter", "vps-starter", "DE-fra", 1, 1024, 20, 1000, 499, "USD", BillingMonthly, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Slug() != "vps-starter" {
		t.Fatalf("expected slug vps-starter, got %s", p.Slug())
	}
	if p.Location() != "DE-fra" {
		t.Fatalf("expected location DE-fra, got %s", p.Location())
	}
	if !p.Enabled() {
		t.Fatal("expected enabled by default")
	}
	if p.TotalSlots() != 10 {
		t.Fatalf("expected total slots 10, got %d", p.TotalSlots())
	}
	if p.SoldSlots() != 0 {
		t.Fatalf("expected sold slots 0, got %d", p.SoldSlots())
	}
	if p.AvailableSlots() != 10 {
		t.Fatalf("expected available slots 10, got %d", p.AvailableSlots())
	}
}

func TestProduct_DisableEnable(t *testing.T) {
	p, _ := NewProduct("p-2", "VPS Pro", "vps-pro", "US-nyc", 4, 8192, 100, 5000, 1999, "USD", BillingMonthly, 0)
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
	p, _ := NewProduct("p-3", "VPS Basic", "vps-basic", "EU-ams", 2, 2048, 40, 2000, 999, "EUR", BillingMonthly, 5)
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

func TestProduct_ConsumeAndReleaseSlot(t *testing.T) {
	p, _ := NewProduct("p-4", "VPS Slot", "vps-slot", "US-nyc", 2, 2048, 40, 2000, 999, "USD", BillingMonthly, 3)

	// Consume all 3 slots
	for i := 0; i < 3; i++ {
		if err := p.ConsumeSlot(); err != nil {
			t.Fatalf("consume slot %d: unexpected error: %v", i+1, err)
		}
	}
	if p.SoldSlots() != 3 {
		t.Fatalf("expected sold slots 3, got %d", p.SoldSlots())
	}
	if p.AvailableSlots() != 0 {
		t.Fatalf("expected available slots 0, got %d", p.AvailableSlots())
	}

	// Consuming beyond capacity should fail
	if err := p.ConsumeSlot(); err == nil {
		t.Fatal("expected error when no slots available")
	}

	// Release one slot
	if err := p.ReleaseSlot(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.AvailableSlots() != 1 {
		t.Fatalf("expected available slots 1, got %d", p.AvailableSlots())
	}
}

func TestProduct_SetTotalSlots(t *testing.T) {
	p, _ := NewProduct("p-5", "VPS Stock", "vps-stock", "EU-ams", 1, 1024, 20, 1000, 499, "USD", BillingMonthly, 5)

	// Sell 3 slots
	for i := 0; i < 3; i++ {
		_ = p.ConsumeSlot()
	}

	// Adjust total up --?OK
	if err := p.SetTotalSlots(10); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.TotalSlots() != 10 {
		t.Fatalf("expected 10, got %d", p.TotalSlots())
	}
	if p.AvailableSlots() != 7 {
		t.Fatalf("expected 7 available, got %d", p.AvailableSlots())
	}

	// Adjust total below sold --?should fail
	if err := p.SetTotalSlots(2); err == nil {
		t.Fatal("expected error when total < sold")
	}

	// Negative total (< -1) --?should fail
	if err := p.SetTotalSlots(-2); err == nil {
		t.Fatal("expected error for total slots < -1")
	}
}

func TestProduct_ReleaseSlotWhenNoneSold(t *testing.T) {
	p, _ := NewProduct("p-6", "VPS Empty", "vps-empty", "US-lax", 1, 512, 10, 500, 299, "USD", BillingMonthly, 5)
	if err := p.ReleaseSlot(); err == nil {
		t.Fatal("expected error when releasing with 0 sold slots")
	}
}

func TestNewProduct_NegativeTotalSlots(t *testing.T) {
	_, err := NewProduct("p-7", "VPS Bad", "vps-bad", "US-nyc", 1, 1024, 20, 1000, 499, "USD", BillingMonthly, -2)
	if err == nil {
		t.Fatal("expected error for total slots < -1")
	}
}

func TestNewProduct_UnlimitedSlots(t *testing.T) {
	p, err := NewProduct("p-8", "VPS Unlimited", "vps-unlimited", "US-nyc", 1, 1024, 20, 1000, 499, "USD", BillingMonthly, UnlimitedSlots)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.IsUnlimited() {
		t.Fatal("expected product to be unlimited")
	}
	if p.AvailableSlots() != UnlimitedSlots {
		t.Fatalf("expected available slots %d (unlimited) for unlimited product, got %d", UnlimitedSlots, p.AvailableSlots())
	}

	// Should be able to consume slots without limit
	for i := 0; i < 100; i++ {
		if err := p.ConsumeSlot(); err != nil {
			t.Fatalf("consume slot %d: unexpected error: %v", i+1, err)
		}
	}
	if p.SoldSlots() != 100 {
		t.Fatalf("expected sold slots 100, got %d", p.SoldSlots())
	}
	// AvailableSlots stays UnlimitedSlots even after consuming
	if p.AvailableSlots() != UnlimitedSlots {
		t.Fatalf("expected available slots %d (unlimited) for unlimited product after sales, got %d", UnlimitedSlots, p.AvailableSlots())
	}
}

func TestProduct_SetTotalSlots_Unlimited(t *testing.T) {
	p, _ := NewProduct("p-9", "VPS Switch", "vps-switch", "US-nyc", 1, 1024, 20, 1000, 499, "USD", BillingMonthly, 5)

	// Sell 3 slots
	for i := 0; i < 3; i++ {
		_ = p.ConsumeSlot()
	}

	// Switch to unlimited --?should succeed even though sold > 0
	if err := p.SetTotalSlots(UnlimitedSlots); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.IsUnlimited() {
		t.Fatal("expected product to be unlimited")
	}
	if p.AvailableSlots() != UnlimitedSlots {
		t.Fatalf("expected available slots %d (unlimited) for unlimited product, got %d", UnlimitedSlots, p.AvailableSlots())
	}

	// Switch back to finite --?must be >= sold slots
	if err := p.SetTotalSlots(2); err == nil {
		t.Fatal("expected error when total < sold")
	}
	if err := p.SetTotalSlots(5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.AvailableSlots() != 2 {
		t.Fatalf("expected 2 available, got %d", p.AvailableSlots())
	}
}
