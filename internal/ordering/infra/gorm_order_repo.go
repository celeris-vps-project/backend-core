package infra

import (
	"backend-core/internal/ordering/domain"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ---- Persistence Object ----

type OrderPO struct {
	ID           string     `gorm:"primaryKey;column:id"`
	CustomerID   string     `gorm:"index;column:customer_id"`
	ProductID    string     `gorm:"index;column:product_id"`
	InvoiceID    string     `gorm:"index;column:invoice_id"`
	BillingCycle string     `gorm:"column:billing_cycle;default:one_time"`
	Status       string     `gorm:"column:status"`
	Currency     string     `gorm:"column:currency"`
	PriceAmount  int64      `gorm:"column:price_amount"`
	Hostname     string     `gorm:"column:hostname"`
	Plan         string     `gorm:"column:plan"`
	Region       string     `gorm:"column:region"`
	OS           string     `gorm:"column:os"`
	CPU          int        `gorm:"column:cpu"`
	MemoryMB     int        `gorm:"column:memory_mb"`
	DiskGB       int        `gorm:"column:disk_gb"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	ActivatedAt  *time.Time `gorm:"column:activated_at"`
	SuspendedAt  *time.Time `gorm:"column:suspended_at"`
	CancelledAt  *time.Time `gorm:"column:cancelled_at"`
	TerminatedAt *time.Time `gorm:"column:terminated_at"`
	CancelReason string     `gorm:"column:cancel_reason"`
}

func (OrderPO) TableName() string { return "orders" }

// ---- Repository ----

// GormOrderRepo implements domain.OrderRepository using GORM.
// It is driver-agnostic: works with SQLite, PostgreSQL, or any GORM-supported database.
type GormOrderRepo struct {
	db *gorm.DB
}

func NewGormOrderRepo(db *gorm.DB) *GormOrderRepo {
	return &GormOrderRepo{db: db}
}

func (r *GormOrderRepo) GetByID(id string) (*domain.Order, error) {
	var po OrderPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("order not found")
		}
		return nil, err
	}
	return orderToDomain(po), nil
}

func (r *GormOrderRepo) ListAll() ([]*domain.Order, error) {
	var pos []OrderPO
	if err := r.db.Find(&pos).Error; err != nil {
		return nil, err
	}
	orders := make([]*domain.Order, len(pos))
	for i, po := range pos {
		orders[i] = orderToDomain(po)
	}
	return orders, nil
}

func (r *GormOrderRepo) ListByCustomerID(customerID string) ([]*domain.Order, error) {
	var pos []OrderPO
	if err := r.db.Where("customer_id = ?", customerID).Find(&pos).Error; err != nil {
		return nil, err
	}
	orders := make([]*domain.Order, len(pos))
	for i, po := range pos {
		orders[i] = orderToDomain(po)
	}
	return orders, nil
}

func (r *GormOrderRepo) Save(order *domain.Order) error {
	po := orderFromDomain(order)
	return r.db.Save(&po).Error
}

// ---- Mapping helpers ----

func orderToDomain(po OrderPO) *domain.Order {
	cfg, _ := domain.NewVPSConfig(
		po.Hostname, po.Plan, po.Region, po.OS,
		po.CPU, po.MemoryMB, po.DiskGB,
	)
	return domain.ReconstituteOrder(
		po.ID, po.CustomerID, po.ProductID, po.InvoiceID, po.BillingCycle,
		cfg,
		po.Status, po.Currency, po.PriceAmount,
		po.CreatedAt,
		po.ActivatedAt, po.SuspendedAt, po.CancelledAt, po.TerminatedAt,
		po.CancelReason,
	)
}

func orderFromDomain(o *domain.Order) OrderPO {
	cfg := o.VPSConfig()
	return OrderPO{
		ID:           o.ID(),
		CustomerID:   o.CustomerID(),
		ProductID:    o.ProductID(),
		InvoiceID:    o.InvoiceID(),
		BillingCycle: o.BillingCycle(),
		Status:       o.Status(),
		Currency:     o.Currency(),
		PriceAmount:  o.PriceAmount(),
		Hostname:     cfg.Hostname(),
		Plan:         cfg.Plan(),
		Region:       cfg.Region(),
		OS:           cfg.OS(),
		CPU:          cfg.CPU(),
		MemoryMB:     cfg.MemoryMB(),
		DiskGB:       cfg.DiskGB(),
		CreatedAt:    o.CreatedAt(),
		ActivatedAt:  o.ActivatedAt(),
		SuspendedAt:  o.SuspendedAt(),
		CancelledAt:  o.CancelledAt(),
		TerminatedAt: o.TerminatedAt(),
		CancelReason: o.CancelReason(),
	}
}
