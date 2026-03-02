package domain

// InvoiceRepository provides persistence for invoices.
type InvoiceRepository interface {
	GetByID(id string) (*Invoice, error)
	ListByCustomerID(customerID string) ([]*Invoice, error)
	Save(invoice *Invoice) error
}
