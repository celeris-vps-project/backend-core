package app

import (
	"backend-core/internal/billing/domain"
	"errors"
	"time"
)

// IDGenerator abstracts invoice id creation.
type IDGenerator interface {
	NewID() string
}

// PaymentGateway is reserved for future gateway implementations.
type PaymentGateway interface {
	Charge(invoice *domain.Invoice, amount domain.Money) (PaymentReceipt, error)
	Refund(receiptID string, amount domain.Money) error
}

// PaymentReceipt records the gateway response.
type PaymentReceipt struct {
	ID     string
	PaidAt time.Time
}

// InvoiceAppService orchestrates invoice workflows.
type InvoiceAppService struct {
	repo    domain.InvoiceRepository
	ids     IDGenerator
	gateway PaymentGateway
}

func NewInvoiceAppService(repo domain.InvoiceRepository, ids IDGenerator, gateway PaymentGateway) *InvoiceAppService {
	return &InvoiceAppService{repo: repo, ids: ids, gateway: gateway}
}

func (s *InvoiceAppService) CreateDraft(customerID, currency string) (*domain.Invoice, error) {
	if s.ids == nil {
		return nil, errors.New("app_error: id generator is required")
	}
	id := s.ids.NewID()
	invoice, err := domain.NewDraftInvoice(id, customerID, currency)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Save(invoice); err != nil {
		return nil, err
	}
	return invoice, nil
}

func (s *InvoiceAppService) AddLineItem(invoiceID string, item domain.LineItem) error {
	invoice, err := s.repo.GetByID(invoiceID)
	if err != nil {
		return err
	}
	if err := invoice.AddLineItem(item); err != nil {
		return err
	}
	return s.repo.Save(invoice)
}

func (s *InvoiceAppService) SetTaxAmount(invoiceID string, tax domain.Money) error {
	invoice, err := s.repo.GetByID(invoiceID)
	if err != nil {
		return err
	}
	if err := invoice.SetTaxAmount(tax); err != nil {
		return err
	}
	return s.repo.Save(invoice)
}

func (s *InvoiceAppService) IssueInvoice(invoiceID string, issuedAt time.Time, dueAt *time.Time) error {
	invoice, err := s.repo.GetByID(invoiceID)
	if err != nil {
		return err
	}
	if err := invoice.Issue(issuedAt, dueAt); err != nil {
		return err
	}
	return s.repo.Save(invoice)
}

func (s *InvoiceAppService) RecordPayment(invoiceID string, amount domain.Money, paidAt time.Time) error {
	invoice, err := s.repo.GetByID(invoiceID)
	if err != nil {
		return err
	}
	if err := invoice.RecordPayment(amount, paidAt); err != nil {
		return err
	}
	return s.repo.Save(invoice)
}

func (s *InvoiceAppService) GetInvoice(invoiceID string) (*domain.Invoice, error) {
	return s.repo.GetByID(invoiceID)
}

func (s *InvoiceAppService) ListByCustomer(customerID string) ([]*domain.Invoice, error) {
	return s.repo.ListByCustomerID(customerID)
}

func (s *InvoiceAppService) VoidInvoice(invoiceID, reason string) error {
	invoice, err := s.repo.GetByID(invoiceID)
	if err != nil {
		return err
	}
	if err := invoice.Void(reason); err != nil {
		return err
	}
	return s.repo.Save(invoice)
}

func (s *InvoiceAppService) CollectPayment(invoiceID string, amount domain.Money) (PaymentReceipt, error) {
	if s.gateway == nil {
		return PaymentReceipt{}, errors.New("app_error: payment gateway not configured")
	}
	invoice, err := s.repo.GetByID(invoiceID)
	if err != nil {
		return PaymentReceipt{}, err
	}
	receipt, err := s.gateway.Charge(invoice, amount)
	if err != nil {
		return PaymentReceipt{}, err
	}
	if err := invoice.RecordPayment(amount, receipt.PaidAt); err != nil {
		return PaymentReceipt{}, err
	}
	if err := s.repo.Save(invoice); err != nil {
		return PaymentReceipt{}, err
	}
	return receipt, nil
}
