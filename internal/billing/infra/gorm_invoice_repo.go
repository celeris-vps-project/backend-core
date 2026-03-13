package infra

import (
	"backend-core/internal/billing/domain"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ---- Persistence Objects ----

type InvoicePO struct {
	ID           string       `gorm:"primaryKey;column:id"`
	CustomerID   string       `gorm:"index;column:customer_id"`
	Currency     string       `gorm:"column:currency"`
	Status       string       `gorm:"column:status"`
	BillingCycle string       `gorm:"column:billing_cycle;default:one_time"`
	PeriodStart  *time.Time   `gorm:"column:period_start"`
	PeriodEnd    *time.Time   `gorm:"column:period_end"`
	Subtotal     int64        `gorm:"column:subtotal"`
	Tax          int64        `gorm:"column:tax"`
	Total        int64        `gorm:"column:total"`
	AmountPaid   int64        `gorm:"column:amount_paid"`
	IssuedAt     *time.Time   `gorm:"column:issued_at"`
	DueAt        *time.Time   `gorm:"column:due_at"`
	PaidAt       *time.Time   `gorm:"column:paid_at"`
	VoidReason   string       `gorm:"column:void_reason"`
	LineItems    []LineItemPO `gorm:"foreignKey:InvoiceID;references:ID"`
}

func (InvoicePO) TableName() string { return "invoices" }

type LineItemPO struct {
	ID          string `gorm:"primaryKey;column:id"`
	InvoiceID   string `gorm:"index;column:invoice_id"`
	Description string `gorm:"column:description"`
	Quantity    int64  `gorm:"column:quantity"`
	UnitPrice   int64  `gorm:"column:unit_price"`
	Currency    string `gorm:"column:currency"`
}

func (LineItemPO) TableName() string { return "invoice_line_items" }

// ---- Repository ----

// GormInvoiceRepo implements domain.InvoiceRepository using GORM.
// It is driver-agnostic: works with SQLite, PostgreSQL, or any GORM-supported database.
type GormInvoiceRepo struct {
	db *gorm.DB
}

func NewGormInvoiceRepo(db *gorm.DB) *GormInvoiceRepo {
	return &GormInvoiceRepo{db: db}
}

func (r *GormInvoiceRepo) GetByID(id string) (*domain.Invoice, error) {
	var po InvoicePO
	if err := r.db.Preload("LineItems").Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invoice not found")
		}
		return nil, err
	}
	return toDomain(po), nil
}

func (r *GormInvoiceRepo) ListByCustomerID(customerID string) ([]*domain.Invoice, error) {
	var pos []InvoicePO
	if err := r.db.Preload("LineItems").Where("customer_id = ?", customerID).Find(&pos).Error; err != nil {
		return nil, err
	}
	invoices := make([]*domain.Invoice, len(pos))
	for i, po := range pos {
		invoices[i] = toDomain(po)
	}
	return invoices, nil
}

func (r *GormInvoiceRepo) ExistsByID(id string) (bool, error) {
	var count int64
	if err := r.db.Model(&InvoicePO{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *GormInvoiceRepo) Save(invoice *domain.Invoice) error {
	po := fromDomain(invoice)

	return r.db.Transaction(func(tx *gorm.DB) error {
		// Upsert the invoice header
		if err := tx.Save(&po).Error; err != nil {
			return err
		}
		// Replace line items: delete old, insert new
		if err := tx.Where("invoice_id = ?", po.ID).Delete(&LineItemPO{}).Error; err != nil {
			return err
		}
		for _, li := range po.LineItems {
			if err := tx.Create(&li).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- Mapping helpers ----

func toDomain(po InvoicePO) *domain.Invoice {
	items := make([]domain.LineItem, len(po.LineItems))
	for i, li := range po.LineItems {
		unitPrice := domain.ZeroMoney(li.Currency)
		if m, err := domain.NewMoney(li.Currency, li.UnitPrice); err == nil {
			unitPrice = m
		}
		if item, err := domain.NewLineItem(li.ID, li.Description, li.Quantity, unitPrice); err == nil {
			items[i] = item
		}
	}

	// Reconstruct billing cycle; default to one_time for legacy rows.
	cycleType := po.BillingCycle
	if cycleType == "" {
		cycleType = domain.BillingCycleOneTime
	}
	billingCycle, _ := domain.NewBillingCycle(cycleType)

	subtotal, _ := domain.NewMoney(po.Currency, po.Subtotal)
	tax, _ := domain.NewMoney(po.Currency, po.Tax)
	total, _ := domain.NewMoney(po.Currency, po.Total)
	amountPaid, _ := domain.NewMoney(po.Currency, po.AmountPaid)

	return domain.ReconstituteInvoice(
		po.ID, po.CustomerID, po.Currency, po.Status,
		billingCycle,
		po.PeriodStart, po.PeriodEnd,
		items,
		subtotal, tax, total, amountPaid,
		po.IssuedAt, po.DueAt, po.PaidAt,
		po.VoidReason,
	)
}

func fromDomain(inv *domain.Invoice) InvoicePO {
	lineItems := make([]LineItemPO, len(inv.LineItems()))
	for i, li := range inv.LineItems() {
		lineItems[i] = LineItemPO{
			ID:          li.ID(),
			InvoiceID:   inv.ID(),
			Description: li.Description(),
			Quantity:    li.Quantity(),
			UnitPrice:   li.UnitPrice().Amount(),
			Currency:    li.UnitPrice().Currency(),
		}
	}

	return InvoicePO{
		ID:           inv.ID(),
		CustomerID:   inv.CustomerID(),
		Currency:     inv.Currency(),
		Status:       inv.Status(),
		BillingCycle: inv.BillingCycle().Type(),
		PeriodStart:  inv.PeriodStart(),
		PeriodEnd:    inv.PeriodEnd(),
		Subtotal:     inv.Subtotal().Amount(),
		Tax:          inv.Tax().Amount(),
		Total:        inv.Total().Amount(),
		AmountPaid:   inv.AmountPaid().Amount(),
		IssuedAt:     inv.IssuedAt(),
		DueAt:        inv.DueAt(),
		PaidAt:       inv.PaidAt(),
		VoidReason:   inv.VoidReason(),
		LineItems:    lineItems,
	}
}
