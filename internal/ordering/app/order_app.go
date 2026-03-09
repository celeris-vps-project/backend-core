package app

import (
	"backend-core/internal/ordering/domain"
	"errors"
	"time"
)

// IDGenerator abstracts order id creation.
type IDGenerator interface {
	NewID() string
}

// OrderAppService orchestrates order workflows.
type OrderAppService struct {
	repo         domain.OrderRepository
	ids          IDGenerator
	provisioning domain.ProvisioningService // nil until a provider is wired
}

func NewOrderAppService(repo domain.OrderRepository, ids IDGenerator, prov domain.ProvisioningService) *OrderAppService {
	return &OrderAppService{repo: repo, ids: ids, provisioning: prov}
}

// CreateOrder places a new VPS order in "pending" status.
func (s *OrderAppService) CreateOrder(
	customerID, productID, invoiceID string,
	cfg domain.VPSConfig,
	currency string,
	priceAmount int64,
) (*domain.Order, error) {
	if s.ids == nil {
		return nil, errors.New("app_error: id generator is required")
	}
	id := s.ids.NewID()
	order, err := domain.NewOrder(id, customerID, productID, invoiceID, cfg, currency, priceAmount)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Save(order); err != nil {
		return nil, err
	}
	return order, nil
}

// GetOrder retrieves an order by id.
func (s *OrderAppService) GetOrder(orderID string) (*domain.Order, error) {
	return s.repo.GetByID(orderID)
}

// ListByCustomer returns all orders for a customer.
func (s *OrderAppService) ListByCustomer(customerID string) ([]*domain.Order, error) {
	return s.repo.ListByCustomerID(customerID)
}

// ActivateOrder moves an order to active and optionally calls provisioning.
func (s *OrderAppService) ActivateOrder(orderID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := order.Activate(now); err != nil {
		return err
	}
	if s.provisioning != nil {
		if err := s.provisioning.Provision(order); err != nil {
			return err
		}
	}
	return s.repo.Save(order)
}

// SuspendOrder suspends an active order and optionally calls provisioning.
func (s *OrderAppService) SuspendOrder(orderID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := order.Suspend(now); err != nil {
		return err
	}
	if s.provisioning != nil {
		if err := s.provisioning.Suspend(order); err != nil {
			return err
		}
	}
	return s.repo.Save(order)
}

// UnsuspendOrder re-activates a suspended order.
func (s *OrderAppService) UnsuspendOrder(orderID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := order.Unsuspend(now); err != nil {
		return err
	}
	if s.provisioning != nil {
		if err := s.provisioning.Unsuspend(order); err != nil {
			return err
		}
	}
	return s.repo.Save(order)
}

// CancelOrder cancels an order (customer-initiated).
func (s *OrderAppService) CancelOrder(orderID, reason string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := order.Cancel(reason, now); err != nil {
		return err
	}
	if s.provisioning != nil {
		_ = s.provisioning.Deprovision(order) // best-effort
	}
	return s.repo.Save(order)
}

// TerminateOrder terminates an order (admin-initiated).
func (s *OrderAppService) TerminateOrder(orderID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := order.Terminate(now); err != nil {
		return err
	}
	if s.provisioning != nil {
		_ = s.provisioning.Deprovision(order)
	}
	return s.repo.Save(order)
}
