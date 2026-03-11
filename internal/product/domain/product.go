package domain

import (
	"errors"
)

// UnlimitedSlots is the sentinel value for unlimited inventory.
const UnlimitedSlots = -1

// BillingCycle represents how frequently a product is billed.
type BillingCycle string

const (
	BillingMonthly   BillingCycle = "monthly"
	BillingQuarterly BillingCycle = "quarterly"
	BillingAnnually  BillingCycle = "annually"
)

// DomainEvent is the marker interface for events raised within the Product aggregate.
type DomainEvent interface {
	EventName() string
}

// Product defines a VPS plan available for purchase.
//
// Key concept: total_slots is the COMMERCIAL inventory — how many units the
// admin wants to sell. This is independent of the physical capacity of the
// underlying node resource pool. The admin may intentionally over-sell
// (with a warning) and excess orders enter a pending-provisioning queue.
//
// A Product is mapped to a regionID (resource pool) rather than a single
// node, enabling load-balancing across all nodes in that region.
type Product struct {
	id             string
	name           string // e.g. "VPS Starter"
	slug           string // e.g. "vps-starter"
	location       string // e.g. "DE-fra" — legacy display label
	regionID       string // FK to Region — legacy
	resourcePoolID string // FK to ResourcePool — determines the resource pool for provisioning
	cpu            int
	memoryMB       int
	diskGB         int
	bandwidthGB    int
	priceAmount    int64 // minor units per cycle
	currency       string
	billingCycle   BillingCycle
	enabled        bool
	sortOrder      int
	totalSlots     int // commercial inventory (how many units admin wants to sell)
	soldSlots      int // number of slots already sold / allocated

	// domainEvents collects events raised by this aggregate during a use-case.
	domainEvents []DomainEvent
}

func NewProduct(id, name, slug, location string, cpu, memoryMB, diskGB, bandwidthGB int, priceAmount int64, currency string, cycle BillingCycle, totalSlots int) (*Product, error) {
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
	if totalSlots < UnlimitedSlots {
		return nil, errors.New("domain_error: total slots must be >= -1 (-1 = unlimited)")
	}
	return &Product{
		id: id, name: name, slug: slug, location: location,
		cpu: cpu, memoryMB: memoryMB, diskGB: diskGB, bandwidthGB: bandwidthGB,
		priceAmount: priceAmount, currency: currency, billingCycle: cycle,
		enabled: true, sortOrder: 0, totalSlots: totalSlots, soldSlots: 0,
	}, nil
}

func ReconstituteProduct(id, name, slug, location, regionID, resourcePoolID string, cpu, memoryMB, diskGB, bandwidthGB int, priceAmount int64, currency string, cycle BillingCycle, enabled bool, sortOrder, totalSlots, soldSlots int) *Product {
	return &Product{
		id: id, name: name, slug: slug, location: location, regionID: regionID,
		resourcePoolID: resourcePoolID,
		cpu:            cpu, memoryMB: memoryMB, diskGB: diskGB, bandwidthGB: bandwidthGB,
		priceAmount: priceAmount, currency: currency, billingCycle: cycle,
		enabled: enabled, sortOrder: sortOrder, totalSlots: totalSlots, soldSlots: soldSlots,
	}
}

func (p *Product) ID() string                 { return p.id }
func (p *Product) Name() string               { return p.name }
func (p *Product) Slug() string               { return p.slug }
func (p *Product) Location() string           { return p.location }
func (p *Product) RegionID() string           { return p.regionID }
func (p *Product) CPU() int                   { return p.cpu }
func (p *Product) MemoryMB() int              { return p.memoryMB }
func (p *Product) DiskGB() int                { return p.diskGB }
func (p *Product) BandwidthGB() int           { return p.bandwidthGB }
func (p *Product) PriceAmount() int64         { return p.priceAmount }
func (p *Product) Currency() string           { return p.currency }
func (p *Product) BillingCycle() BillingCycle { return p.billingCycle }
func (p *Product) Enabled() bool              { return p.enabled }
func (p *Product) SortOrder() int             { return p.sortOrder }
func (p *Product) TotalSlots() int            { return p.totalSlots }
func (p *Product) SoldSlots() int             { return p.soldSlots }
func (p *Product) IsUnlimited() bool          { return p.totalSlots == UnlimitedSlots }
func (p *Product) AvailableSlots() int {
	if p.IsUnlimited() {
		return UnlimitedSlots
	}
	return p.totalSlots - p.soldSlots
}

func (p *Product) SetRegionID(regionID string)     { p.regionID = regionID }
func (p *Product) ResourcePoolID() string          { return p.resourcePoolID }
func (p *Product) SetResourcePoolID(poolID string) { p.resourcePoolID = poolID }

func (p *Product) Enable()            { p.enabled = true }
func (p *Product) Disable()           { p.enabled = false }
func (p *Product) SetSortOrder(n int) { p.sortOrder = n }

// SetTotalSlots adjusts the commercial inventory slots. Use -1 for unlimited.
// The new value must not be less than the number of slots already sold
// (unless setting to unlimited).
func (p *Product) SetTotalSlots(n int) error {
	if n < UnlimitedSlots {
		return errors.New("domain_error: total slots must be >= -1 (-1 = unlimited)")
	}
	if n != UnlimitedSlots && n < p.soldSlots {
		return errors.New("domain_error: total slots cannot be less than sold slots")
	}
	p.totalSlots = n
	return nil
}

// ConsumeSlot decrements an available slot (e.g. on purchase).
func (p *Product) ConsumeSlot() error {
	if !p.IsUnlimited() && p.soldSlots >= p.totalSlots {
		return errors.New("domain_error: no available slots")
	}
	p.soldSlots++
	return nil
}

// ReleaseSlot increments an available slot (e.g. on cancellation).
func (p *Product) ReleaseSlot() error {
	if p.soldSlots <= 0 {
		return errors.New("domain_error: no sold slots to release")
	}
	p.soldSlots--
	return nil
}

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

// ---- Domain Event support ----

// RaiseEvent records a domain event on the aggregate. These are collected
// by the application service after persistence and published to the event bus.
func (p *Product) RaiseEvent(e DomainEvent) {
	p.domainEvents = append(p.domainEvents, e)
}

// CollectEvents returns and clears all pending domain events.
func (p *Product) CollectEvents() []DomainEvent {
	events := p.domainEvents
	p.domainEvents = nil
	return events
}
