package app

import (
	"fmt"
	"time"
)

type RenewalInvoice struct {
	ID        string
	Status    string
	PeriodEnd *time.Time
	DueAt     *time.Time
}

type RenewalInstance struct {
	ID     string
	Status string
}

type RenewalOrderManager interface {
	ListOrders() ([]PayableOrder, error)
	SuspendOrder(orderID string) error
	UnsuspendOrder(orderID string) error
	ReplaceInvoice(orderID, invoiceID string) error
}

type RenewalInvoiceManager interface {
	GetInvoice(invoiceID string) (RenewalInvoice, error)
	GenerateRenewalInvoice(sourceInvoiceID string) (RenewalInvoice, error)
	IssueInvoice(invoiceID string, issuedAt time.Time, dueAt *time.Time) error
	RecordInvoicePayment(invoiceID string, amount int64, currency string) error
}

type RenewalInstanceManager interface {
	GetByOrderID(orderID string) (RenewalInstance, error)
	SuspendInstance(instanceID string) error
	RecoverFromBillingSuspension(instanceID string) error
}

type RenewalService struct {
	orders    RenewalOrderManager
	invoices  RenewalInvoiceManager
	instances RenewalInstanceManager
}

func NewRenewalService(
	orders RenewalOrderManager,
	invoices RenewalInvoiceManager,
	instances RenewalInstanceManager,
) *RenewalService {
	return &RenewalService{
		orders:    orders,
		invoices:  invoices,
		instances: instances,
	}
}

func (s *RenewalService) PreparePayment(order PayableOrder) (string, error) {
	if !isRenewableOrder(order) {
		return "", fmt.Errorf("renewal payment is not available for order status=%s cycle=%s", order.Status, order.BillingCycle)
	}
	if order.InvoiceID == "" {
		return "", fmt.Errorf("no linked renewal invoice for order %s", order.ID)
	}
	invoice, err := s.invoices.GetInvoice(order.InvoiceID)
	if err != nil {
		return "", err
	}
	if invoice.Status != "issued" {
		return "", fmt.Errorf("no outstanding renewal invoice for order %s", order.ID)
	}
	return invoice.ID, nil
}

func (s *RenewalService) HandlePaidOrder(order PayableOrder) error {
	if !isRenewableOrder(order) {
		return fmt.Errorf("order %s is not eligible for renewal payment handling", order.ID)
	}
	if order.InvoiceID == "" {
		return fmt.Errorf("order %s has no linked renewal invoice", order.ID)
	}
	invoice, err := s.invoices.GetInvoice(order.InvoiceID)
	if err != nil {
		return err
	}
	if invoice.Status == "paid" {
		return nil
	}
	if err := s.invoices.RecordInvoicePayment(order.InvoiceID, order.PriceAmount, order.Currency); err != nil {
		return err
	}
	if order.Status != "suspended" {
		return nil
	}
	if err := s.orders.UnsuspendOrder(order.ID); err != nil {
		return err
	}
	if s.instances == nil {
		return nil
	}
	instance, err := s.instances.GetByOrderID(order.ID)
	if err != nil {
		return nil
	}
	return s.instances.RecoverFromBillingSuspension(instance.ID)
}

func (s *RenewalService) RunCycle(now time.Time, leadDays int) error {
	if leadDays < 0 {
		leadDays = 0
	}
	orders, err := s.orders.ListOrders()
	if err != nil {
		return err
	}
	for _, order := range orders {
		if order.BillingCycle != "monthly" {
			continue
		}
		if order.Status != "active" && order.Status != "suspended" {
			continue
		}
		if order.InvoiceID == "" {
			continue
		}
		invoice, err := s.invoices.GetInvoice(order.InvoiceID)
		if err != nil || invoice.PeriodEnd == nil {
			continue
		}

		switch invoice.Status {
		case "paid":
			issueAt := invoice.PeriodEnd.AddDate(0, 0, -leadDays)
			if now.Before(issueAt) {
				continue
			}
			renewal, err := s.invoices.GenerateRenewalInvoice(invoice.ID)
			if err != nil {
				continue
			}
			if renewal.Status == "draft" {
				dueAt := *invoice.PeriodEnd
				if err := s.invoices.IssueInvoice(renewal.ID, now, &dueAt); err != nil {
					continue
				}
			}
			if order.InvoiceID != renewal.ID {
				_ = s.orders.ReplaceInvoice(order.ID, renewal.ID)
			}

		case "issued":
			if order.Status != "active" || now.Before(*invoice.PeriodEnd) {
				continue
			}
			if err := s.orders.SuspendOrder(order.ID); err != nil {
				continue
			}
			if s.instances == nil {
				continue
			}
			instance, err := s.instances.GetByOrderID(order.ID)
			if err != nil {
				continue
			}
			_ = s.instances.SuspendInstance(instance.ID)
		}
	}
	return nil
}

func isRenewableOrder(order PayableOrder) bool {
	if order.BillingCycle != "monthly" {
		return false
	}
	return order.Status == "active" || order.Status == "suspended"
}
