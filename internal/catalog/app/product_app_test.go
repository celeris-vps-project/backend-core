package app

import (
	"backend-core/internal/catalog/domain"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"context"
	"errors"
	"fmt"
	"testing"
)

// ---- In-memory test doubles ----

type memProductRepo struct {
	items map[string]*domain.Product
}

func newMemProductRepo() *memProductRepo {
	return &memProductRepo{items: map[string]*domain.Product{}}
}

func (r *memProductRepo) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	p, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return p, nil
}

func (r *memProductRepo) GetBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	for _, p := range r.items {
		if p.Slug() == slug {
			return p, nil
		}
	}
	return nil, errors.New("not found")
}

func (r *memProductRepo) ListAll(ctx context.Context) ([]*domain.Product, error) {
	out := make([]*domain.Product, 0, len(r.items))
	for _, p := range r.items {
		out = append(out, p)
	}
	return out, nil
}

func (r *memProductRepo) ListEnabled(ctx context.Context) ([]*domain.Product, error) {
	var out []*domain.Product
	for _, p := range r.items {
		if p.Enabled() {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *memProductRepo) ListByRegionID(ctx context.Context, regionID string) ([]*domain.Product, error) {
	var out []*domain.Product
	for _, p := range r.items {
		if p.RegionID() == regionID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (r *memProductRepo) ConsumeSlotAtomic(ctx context.Context, productID string) error {
	p, ok := r.items[productID]
	if !ok {
		return fmt.Errorf("product not found")
	}
	return p.ConsumeSlot()
}

func (r *memProductRepo) ReleaseSlotAtomic(ctx context.Context, productID string) error {
	p, ok := r.items[productID]
	if !ok {
		return fmt.Errorf("product not found")
	}
	return p.ReleaseSlot()
}

func (r *memProductRepo) Save(ctx context.Context, p *domain.Product) error {
	r.items[p.ID()] = p
	return nil
}

type staticIDGen struct{ id string }

func (g staticIDGen) NewID() string { return g.id }

type stubCapacityChecker struct {
	slots map[string]int
}

func (c *stubCapacityChecker) AvailablePhysicalSlots(ctx context.Context, regionID string) (int, error) {
	s, ok := c.slots[regionID]
	if !ok {
		return 0, errors.New("region not found")
	}
	return s, nil
}

// ---- Tests ----

func TestProductApp_PurchasePublishesEvent(t *testing.T) {
	ctx := context.Background()
	repo := newMemProductRepo()
	bus := eventbus.New()

	svc := NewProductAppService(repo, staticIDGen{id: "prod-1"}, bus, nil)

	p, err := svc.CreateProduct(ctx, "VPS Starter", "vps-starter", "DE-fra", "region-de", "", "nat", 1, 1024, 20, 1000, 499, "USD", domain.BillingMonthly, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.NetworkMode() != "nat" {
		t.Fatalf("expected nat product mode, got %s", p.NetworkMode())
	}

	var receivedEvent *events.ProductPurchasedEvent
	bus.Subscribe("product.purchased", func(evt eventbus.Event) {
		e := evt.(events.ProductPurchasedEvent)
		receivedEvent = &e
	})

	result, err := svc.PurchaseProduct(ctx, p.ID(), "cust-1", "ord-1", "inst-1", "web-01", "ubuntu-22.04")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SoldSlots() != 1 {
		t.Fatalf("expected sold slots 1, got %d", result.SoldSlots())
	}
	if result.AvailableSlots() != 4 {
		t.Fatalf("expected available slots 4, got %d", result.AvailableSlots())
	}

	if receivedEvent == nil {
		t.Fatal("expected ProductPurchasedEvent to be published")
	}
	if receivedEvent.ProductID != p.ID() {
		t.Fatalf("expected product ID %s, got %s", p.ID(), receivedEvent.ProductID)
	}
	if receivedEvent.RegionID != "region-de" {
		t.Fatalf("expected region region-de, got %s", receivedEvent.RegionID)
	}
	if receivedEvent.CustomerID != "cust-1" {
		t.Fatalf("expected customer cust-1, got %s", receivedEvent.CustomerID)
	}
	if receivedEvent.InstanceID != "inst-1" {
		t.Fatalf("expected instance inst-1, got %s", receivedEvent.InstanceID)
	}
	if receivedEvent.NetworkMode != "nat" {
		t.Fatalf("expected network mode nat, got %s", receivedEvent.NetworkMode)
	}
	if receivedEvent.Hostname != "web-01" {
		t.Fatalf("expected hostname web-01, got %s", receivedEvent.Hostname)
	}
	if receivedEvent.CPU != 1 || receivedEvent.MemoryMB != 1024 || receivedEvent.DiskGB != 20 {
		t.Fatal("event specs don't match product specs")
	}
}

func TestProductApp_PurchaseFailsWhenOutOfStock(t *testing.T) {
	ctx := context.Background()
	repo := newMemProductRepo()
	bus := eventbus.New()
	svc := NewProductAppService(repo, staticIDGen{id: "prod-2"}, bus, nil)

	p, _ := svc.CreateProduct(ctx, "VPS Tiny", "vps-tiny", "US-nyc", "", "", "", 1, 512, 10, 500, 299, "USD", domain.BillingMonthly, 1)

	if _, err := svc.PurchaseProduct(ctx, p.ID(), "cust-1", "ord-1", "", "h1", "ubuntu"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := svc.PurchaseProduct(ctx, p.ID(), "cust-2", "ord-2", "", "h2", "ubuntu"); err == nil {
		t.Fatal("expected error when no slots available")
	}
}

func TestProductApp_PurchaseFailsWhenDisabled(t *testing.T) {
	ctx := context.Background()
	repo := newMemProductRepo()
	bus := eventbus.New()
	svc := NewProductAppService(repo, staticIDGen{id: "prod-3"}, bus, nil)

	p, _ := svc.CreateProduct(ctx, "VPS Off", "vps-off", "DE-fra", "", "", "", 1, 1024, 20, 1000, 499, "USD", domain.BillingMonthly, 10)
	_ = svc.DisableProduct(ctx, p.ID())

	if _, err := svc.PurchaseProduct(ctx, p.ID(), "cust-1", "ord-1", "", "h1", "ubuntu"); err == nil {
		t.Fatal("expected error when product disabled")
	}
}

func TestProductApp_AdjustStock_NormalSave(t *testing.T) {
	ctx := context.Background()
	repo := newMemProductRepo()
	bus := eventbus.New()
	checker := &stubCapacityChecker{slots: map[string]int{"region-de": 50}}
	svc := NewProductAppService(repo, staticIDGen{id: "prod-4"}, bus, checker)

	p, _ := svc.CreateProduct(ctx, "VPS Stock", "vps-stock", "DE-fra", "region-de", "", "", 1, 1024, 20, 1000, 499, "USD", domain.BillingMonthly, 10)

	result, err := svc.AdjustStock(ctx, p.ID(), 30, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Warning {
		t.Fatal("expected no warning when under physical capacity")
	}
	if result.Product.TotalSlots() != 30 {
		t.Fatalf("expected 30 total slots, got %d", result.Product.TotalSlots())
	}
}

func TestProductApp_AdjustStock_WarningWhenExceedsPhysical(t *testing.T) {
	ctx := context.Background()
	repo := newMemProductRepo()
	bus := eventbus.New()
	checker := &stubCapacityChecker{slots: map[string]int{"region-de": 10}}
	svc := NewProductAppService(repo, staticIDGen{id: "prod-5"}, bus, checker)

	p, _ := svc.CreateProduct(ctx, "VPS Over", "vps-over", "DE-fra", "region-de", "", "", 1, 1024, 20, 1000, 499, "USD", domain.BillingMonthly, 5)

	result, err := svc.AdjustStock(ctx, p.ID(), 50, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Warning {
		t.Fatal("expected warning when exceeding physical capacity")
	}
	if !result.RequiresConfirmation {
		t.Fatal("expected requires_confirmation=true")
	}

	stored, _ := repo.GetByID(ctx, p.ID())
	if stored.TotalSlots() != 5 {
		t.Fatalf("expected 5 total slots (not saved), got %d", stored.TotalSlots())
	}

	result2, err := svc.AdjustStock(ctx, p.ID(), 50, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result2.Warning {
		t.Fatal("expected warning flag still present even when confirmed")
	}
	stored2, _ := repo.GetByID(ctx, p.ID())
	if stored2.TotalSlots() != 50 {
		t.Fatalf("expected 50 total slots (saved after confirm), got %d", stored2.TotalSlots())
	}
}

func TestProductApp_SetRegion(t *testing.T) {
	ctx := context.Background()
	repo := newMemProductRepo()
	bus := eventbus.New()
	svc := NewProductAppService(repo, staticIDGen{id: "prod-6"}, bus, nil)

	p, _ := svc.CreateProduct(ctx, "VPS Region", "vps-region", "DE-fra", "", "", "", 1, 1024, 20, 1000, 499, "USD", domain.BillingMonthly, 10)

	if err := svc.SetRegion(ctx, p.ID(), "region-de-fra"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, _ := repo.GetByID(ctx, p.ID())
	if stored.RegionID() != "region-de-fra" {
		t.Fatalf("expected region-de-fra, got %s", stored.RegionID())
	}
}

func TestProductApp_UpdateNetworkMode(t *testing.T) {
	ctx := context.Background()
	repo := newMemProductRepo()
	bus := eventbus.New()
	svc := NewProductAppService(repo, staticIDGen{id: "prod-8"}, bus, nil)

	p, _ := svc.CreateProduct(ctx, "VPS Network", "vps-network", "DE-fra", "", "", "", 1, 1024, 20, 1000, 499, "USD", domain.BillingMonthly, 10)

	if err := svc.UpdateNetworkMode(ctx, p.ID(), "nat"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, _ := repo.GetByID(ctx, p.ID())
	if stored.NetworkMode() != "nat" {
		t.Fatalf("expected nat, got %s", stored.NetworkMode())
	}

	if err := svc.UpdateNetworkMode(ctx, p.ID(), "bridge"); err == nil {
		t.Fatal("expected error for invalid network mode")
	}
}

func TestProductApp_CreateProductRejectsInvalidNetworkMode(t *testing.T) {
	ctx := context.Background()
	repo := newMemProductRepo()
	bus := eventbus.New()
	svc := NewProductAppService(repo, staticIDGen{id: "prod-7"}, bus, nil)

	if _, err := svc.CreateProduct(ctx, "VPS Invalid", "vps-invalid", "DE-fra", "", "", "bridge", 1, 1024, 20, 1000, 499, "USD", domain.BillingMonthly, 1); err == nil {
		t.Fatal("expected error for invalid network mode")
	}
}
