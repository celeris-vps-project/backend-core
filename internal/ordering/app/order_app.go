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
//
// Note: provisioning is NOT handled here. Order state transitions (activate,
// suspend, cancel, terminate) only manage the order lifecycle. Actual resource
// provisioning is driven by domain events through the ProvisionDispatcher in
// the provisioning bounded context (event-driven, see provisioning/app).
type OrderAppService struct {
	repo domain.OrderRepository
	ids  IDGenerator
}

func NewOrderAppService(repo domain.OrderRepository, ids IDGenerator) *OrderAppService {
	return &OrderAppService{repo: repo, ids: ids}
}

// CreateOrder places a new VPS order in "pending" status.
// Callers pass raw VPS parameters; this method constructs the VPSConfig internally
// so that no other context needs to import the ordering domain layer.
func (s *OrderAppService) CreateOrder(
	customerID, productID, invoiceID, billingCycle string,
	hostname, plan, region, os string,
	cpu, memoryMB, diskGB int,
	currency string,
	priceAmount int64,
) (*domain.Order, error) {
	if s.ids == nil {
		return nil, errors.New("app_error: id generator is required")
	}
	cfg, err := domain.NewVPSConfig(hostname, plan, region, os, cpu, memoryMB, diskGB)
	if err != nil {
		return nil, err
	}
	id := s.ids.NewID()
	order, err := domain.NewOrder(id, customerID, productID, invoiceID, billingCycle, cfg, currency, priceAmount)
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

// ListAll returns all orders for background workflows such as recurring billing.
func (s *OrderAppService) ListAll() ([]*domain.Order, error) {
	return s.repo.ListAll()
}

// ListByCustomer returns all orders for a customer.
func (s *OrderAppService) ListByCustomer(customerID string) ([]*domain.Order, error) {
	return s.repo.ListByCustomerID(customerID)
}

// LinkInvoiceToOrder associates a billing invoice with a pending order.
func (s *OrderAppService) LinkInvoiceToOrder(orderID, invoiceID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	if err := order.LinkInvoice(invoiceID); err != nil {
		return err
	}
	return s.repo.Save(order)
}

// ReplaceInvoice updates the outstanding invoice reference for a recurring order.
func (s *OrderAppService) ReplaceInvoice(orderID, invoiceID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	if err := order.ReplaceInvoice(invoiceID); err != nil {
		return err
	}
	return s.repo.Save(order)
}

// ActivateOrder moves an order to active (pending → active).
func (s *OrderAppService) ActivateOrder(orderID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := order.Activate(now); err != nil {
		return err
	}
	return s.repo.Save(order)
}

// SuspendOrder suspends an active order (active → suspended).
func (s *OrderAppService) SuspendOrder(orderID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := order.Suspend(now); err != nil {
		return err
	}
	return s.repo.Save(order)
}

// UnsuspendOrder re-activates a suspended order (suspended → active).
func (s *OrderAppService) UnsuspendOrder(orderID string) error {
	order, err := s.repo.GetByID(orderID)
	if err != nil {
		return err
	}
	now := time.Now()
	if err := order.Unsuspend(now); err != nil {
		return err
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
	return s.repo.Save(order)
}
