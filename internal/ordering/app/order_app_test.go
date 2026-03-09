package app

import (
	"backend-core/internal/ordering/domain"
	"errors"
	"testing"
)

// ---- In-memory test doubles ----

type memoryOrderRepo struct {
	items map[string]*domain.Order
}

func newMemoryOrderRepo() *memoryOrderRepo {
	return &memoryOrderRepo{items: map[string]*domain.Order{}}
}

func (r *memoryOrderRepo) GetByID(id string) (*domain.Order, error) {
	o, ok := r.items[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return o, nil
}

func (r *memoryOrderRepo) ListByCustomerID(customerID string) ([]*domain.Order, error) {
	var result []*domain.Order
	for _, o := range r.items {
		if o.CustomerID() == customerID {
			result = append(result, o)
		}
	}
	return result, nil
}

func (r *memoryOrderRepo) Save(order *domain.Order) error {
	r.items[order.ID()] = order
	return nil
}

type staticIDGen struct{ id string }

func (g staticIDGen) NewID() string { return g.id }

// ---- Tests ----

func TestOrderApp_CreateAndActivate(t *testing.T) {
	repo := newMemoryOrderRepo()
	svc := NewOrderAppService(repo, staticIDGen{id: "ord-100"}, nil)

	cfg, err := domain.NewVPSConfig("web-01", "vps-starter", "us-east-1", "ubuntu-22.04", 2, 2048, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	order, err := svc.CreateOrder("cust-1", "prod-1", "inv-1", cfg, "USD", 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.ID() != "ord-100" {
		t.Fatalf("expected id ord-100, got %s", order.ID())
	}
	if order.Status() != domain.OrderStatusPending {
		t.Fatalf("expected pending, got %s", order.Status())
	}

	if err := svc.ActivateOrder(order.ID()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, _ := repo.GetByID(order.ID())
	if stored.Status() != domain.OrderStatusActive {
		t.Fatalf("expected active, got %s", stored.Status())
	}
}

func TestOrderApp_SuspendAndUnsuspend(t *testing.T) {
	repo := newMemoryOrderRepo()
	svc := NewOrderAppService(repo, staticIDGen{id: "ord-200"}, nil)

	cfg, _ := domain.NewVPSConfig("db-01", "vps-pro", "eu-west-1", "debian-12", 4, 8192, 100)
	_, _ = svc.CreateOrder("cust-2", "prod-2", "inv-2", cfg, "EUR", 1999)

	_ = svc.ActivateOrder("ord-200")
	if err := svc.SuspendOrder("ord-200"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := repo.GetByID("ord-200")
	if stored.Status() != domain.OrderStatusSuspended {
		t.Fatalf("expected suspended, got %s", stored.Status())
	}

	if err := svc.UnsuspendOrder("ord-200"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ = repo.GetByID("ord-200")
	if stored.Status() != domain.OrderStatusActive {
		t.Fatalf("expected active, got %s", stored.Status())
	}
}

func TestOrderApp_Cancel(t *testing.T) {
	repo := newMemoryOrderRepo()
	svc := NewOrderAppService(repo, staticIDGen{id: "ord-300"}, nil)

	cfg, _ := domain.NewVPSConfig("app-01", "vps-starter", "ap-south-1", "centos-9", 1, 1024, 20)
	_, _ = svc.CreateOrder("cust-3", "prod-3", "inv-3", cfg, "USD", 500)

	if err := svc.CancelOrder("ord-300", "changed mind"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stored, _ := repo.GetByID("ord-300")
	if stored.Status() != domain.OrderStatusCancelled {
		t.Fatalf("expected cancelled, got %s", stored.Status())
	}
}
