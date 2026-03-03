package domain

import "errors"

// BillingCycle represents how frequently a product is billed.
type BillingCycle string

const (
	BillingMonthly   BillingCycle = "monthly"
	BillingQuarterly BillingCycle = "quarterly"
	BillingAnnually  BillingCycle = "annually"
)

// Product defines a VPS plan available for purchase.
type Product struct {
	id           string
	name         string // e.g. "VPS Starter"
	slug         string // e.g. "vps-starter"
	cpu          int
	memoryMB     int
	diskGB       int
	bandwidthGB  int
	priceAmount  int64 // minor units per cycle
	currency     string
	billingCycle BillingCycle
	enabled      bool
	sortOrder    int
}

func NewProduct(id, name, slug string, cpu, memoryMB, diskGB, bandwidthGB int, priceAmount int64, currency string, cycle BillingCycle) (*Product, error) {
	if id == "" {
		return nil, errors.New("domain_error: product id is required")
	}
	if name == "" {
		return nil, errors.New("domain_error: name is required")
	}
	if slug == "" {
		return nil, errors.New("domain_error: slug is required")
	}
	if cpu <= 0 {
		return nil, errors.New("domain_error: cpu must be > 0")
	}
	if memoryMB <= 0 {
		return nil, errors.New("domain_error: memory must be > 0")
	}
	if diskGB <= 0 {
		return nil, errors.New("domain_error: disk must be > 0")
	}
	if priceAmount <= 0 {
		return nil, errors.New("domain_error: price must be > 0")
	}
	if currency == "" {
		return nil, errors.New("domain_error: currency is required")
	}
	return &Product{
		id: id, name: name, slug: slug,
		cpu: cpu, memoryMB: memoryMB, diskGB: diskGB, bandwidthGB: bandwidthGB,
		priceAmount: priceAmount, currency: currency, billingCycle: cycle,
		enabled: true, sortOrder: 0,
	}, nil
}

func ReconstituteProduct(id, name, slug string, cpu, memoryMB, diskGB, bandwidthGB int, priceAmount int64, currency string, cycle BillingCycle, enabled bool, sortOrder int) *Product {
	return &Product{
		id: id, name: name, slug: slug,
		cpu: cpu, memoryMB: memoryMB, diskGB: diskGB, bandwidthGB: bandwidthGB,
		priceAmount: priceAmount, currency: currency, billingCycle: cycle,
		enabled: enabled, sortOrder: sortOrder,
	}
}

func (p *Product) ID() string                 { return p.id }
func (p *Product) Name() string               { return p.name }
func (p *Product) Slug() string               { return p.slug }
func (p *Product) CPU() int                   { return p.cpu }
func (p *Product) MemoryMB() int              { return p.memoryMB }
func (p *Product) DiskGB() int                { return p.diskGB }
func (p *Product) BandwidthGB() int           { return p.bandwidthGB }
func (p *Product) PriceAmount() int64         { return p.priceAmount }
func (p *Product) Currency() string           { return p.currency }
func (p *Product) BillingCycle() BillingCycle { return p.billingCycle }
func (p *Product) Enabled() bool              { return p.enabled }
func (p *Product) SortOrder() int             { return p.sortOrder }

func (p *Product) Enable()            { p.enabled = true }
func (p *Product) Disable()           { p.enabled = false }
func (p *Product) SetSortOrder(n int) { p.sortOrder = n }
func (p *Product) SetPrice(amount int64, currency string) error {
	if amount <= 0 {
		return errors.New("domain_error: price must be > 0")
	}
	if currency == "" {
		return errors.New("domain_error: currency is required")
	}
	p.priceAmount = amount
	p.currency = currency
	return nil
}
