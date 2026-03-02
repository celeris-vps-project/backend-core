package domain

// OrderRepository provides persistence for orders.
type OrderRepository interface {
	GetByID(id string) (*Order, error)
	ListByCustomerID(customerID string) ([]*Order, error)
	Save(order *Order) error
}

// ProvisioningService is reserved for future VPS provisioning implementations.
// Implement this interface to integrate with hypervisors / cloud APIs.
type ProvisioningService interface {
	Provision(order *Order) error
	Deprovision(order *Order) error
	Suspend(order *Order) error
	Unsuspend(order *Order) error
}
