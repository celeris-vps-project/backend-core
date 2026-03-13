package domain

// InvoiceRepository provides persistence for invoices.
type InvoiceRepository interface {
	GetByID(id string) (*Invoice, error)
	ListByCustomerID(customerID string) ([]*Invoice, error)
	Save(invoice *Invoice) error

	// ExistsByID returns true if an invoice with the given ID already exists.
	// Used by GenerateRenewalInvoice for idempotency checks.
	ExistsByID(id string) (bool, error)
}
