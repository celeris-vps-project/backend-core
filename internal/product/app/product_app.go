package app

import (
	"backend-core/internal/product/domain"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"fmt"
)

type IDGenerator interface{ NewID() string }

// EventPublisher abstracts the event bus so the app service is testable.
type EventPublisher interface {
	Publish(event eventbus.Event)
}

// RestockResult is returned by AdjustStock to communicate whether a warning
// should be shown to the admin (soft-limit exceeded).
type RestockResult struct {
	Product              *domain.Product
	Warning              bool   // true if commercial stock > physical available
	WarningMessage       string // human-readable warning for frontend confirmation modal
	PhysicalAvailable    int    // current physical slots available in the resource pool
	RequiresConfirmation bool   // frontend should show a confirmation dialog
}

type ProductAppService struct {
	repo            domain.ProductRepository
	ids             IDGenerator
	eventBus        EventPublisher
	capacityChecker domain.PhysicalCapacityChecker // nil = skip physical checks
}

func NewProductAppService(
	repo domain.ProductRepository,
	ids IDGenerator,
	eventBus EventPublisher,
	capacityChecker domain.PhysicalCapacityChecker,
) *ProductAppService {
	return &ProductAppService{
		repo:            repo,
		ids:             ids,
		eventBus:        eventBus,
		capacityChecker: capacityChecker,
	}
}

// CreateProduct creates a new VPS product in the catalog.
func (s *ProductAppService) CreateProduct(name, slug, location, regionID, resourcePoolID string, cpu, memoryMB, diskGB, bandwidthGB int, priceAmount int64, currency string, cycle domain.BillingCycle, totalSlots int) (*domain.Product, error) {
	id := s.ids.NewID()
	p, err := domain.NewProduct(id, name, slug, location, cpu, memoryMB, diskGB, bandwidthGB, priceAmount, currency, cycle, totalSlots)
	if err != nil {
		return nil, err
	}
	if regionID != "" {
		p.SetRegionID(regionID)
	}
	if resourcePoolID != "" {
		p.SetResourcePoolID(resourcePoolID)
	}
	if err := s.repo.Save(p); err != nil {
		return nil, err
	}
	return p, nil
}

// PurchaseProduct processes a customer purchase: consumes a commercial slot
// and publishes a ProductPurchasedEvent for the Node domain to handle
// physical provisioning independently.
func (s *ProductAppService) PurchaseProduct(
	productID, customerID, orderID, hostname, os string,
) (*domain.Product, error) {
	p, err := s.repo.GetByID(productID)
	if err != nil {
		return nil, err
	}
	if !p.Enabled() {
		return nil, fmt.Errorf("app_error: product %s is not available", productID)
	}
	if err := p.ConsumeSlot(); err != nil {
		return nil, err
	}

	// Raise domain event — Node domain will handle provisioning
	p.RaiseEvent(events.ProductPurchasedEvent{
		ProductID:      p.ID(),
		ProductSlug:    p.Slug(),
		RegionID:       p.RegionID(),
		ResourcePoolID: p.ResourcePoolID(),
		CustomerID:     customerID,
		OrderID:        orderID,
		Hostname:       hostname,
		OS:             os,
		CPU:            p.CPU(),
		MemoryMB:       p.MemoryMB(),
		DiskGB:         p.DiskGB(),
	})

	if err := s.repo.Save(p); err != nil {
		return nil, err
	}

	// Publish collected domain events
	s.publishEvents(p)
	return p, nil
}

func (s *ProductAppService) GetProduct(id string) (*domain.Product, error) { return s.repo.GetByID(id) }
func (s *ProductAppService) GetBySlug(slug string) (*domain.Product, error) {
	return s.repo.GetBySlug(slug)
}
func (s *ProductAppService) ListAll() ([]*domain.Product, error)     { return s.repo.ListAll() }
func (s *ProductAppService) ListEnabled() ([]*domain.Product, error) { return s.repo.ListEnabled() }

func (s *ProductAppService) EnableProduct(id string) error {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	p.Enable()
	return s.repo.Save(p)
}

func (s *ProductAppService) DisableProduct(id string) error {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	p.Disable()
	return s.repo.Save(p)
}

func (s *ProductAppService) UpdatePrice(id string, amount int64, currency string) error {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	if err := p.SetPrice(amount, currency); err != nil {
		return err
	}
	return s.repo.Save(p)
}

// AdjustStock sets the commercial inventory slots for a product (admin restocking).
//
// Soft-limit logic:
//   - If totalSlots <= physical available in the resource pool → save normally.
//   - If totalSlots > physical available → return a warning (do NOT block).
//     The frontend should show a confirmation modal. If confirmed=true, save proceeds.
//   - If confirmed=false and warning is triggered, the save is NOT performed —
//     the frontend must re-call with confirmed=true.
func (s *ProductAppService) AdjustStock(id string, totalSlots int, confirmed bool) (*RestockResult, error) {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// Validate the proposed value without mutating yet.
	// SetTotalSlots enforces domain rules (e.g. can't go below sold slots).
	// We do a dry-run: validate, then check physical capacity before committing.
	if totalSlots < domain.UnlimitedSlots {
		return nil, fmt.Errorf("domain_error: total slots must be >= -1 (-1 = unlimited)")
	}
	if totalSlots != domain.UnlimitedSlots && totalSlots < p.SoldSlots() {
		return nil, fmt.Errorf("domain_error: total slots cannot be less than sold slots")
	}

	result := &RestockResult{Product: p}

	// Check physical capacity if a capacity checker is available and product has a region
	if s.capacityChecker != nil && p.RegionID() != "" && totalSlots != domain.UnlimitedSlots {
		// Use resource pool ID if available, otherwise fall back to region ID
		checkID := p.ResourcePoolID()
		if checkID == "" {
			checkID = p.RegionID()
		}
		physAvail, err := s.capacityChecker.AvailablePhysicalSlots(checkID)
		if err == nil {
			result.PhysicalAvailable = physAvail

			// Compare commercial inventory against physical capacity
			if totalSlots > physAvail+p.SoldSlots() {
				result.Warning = true
				result.WarningMessage = fmt.Sprintf(
					"The total product inventory you set (%d) exceeds the physical available quota "+
						"of the current resource pool (%d physical slots available). "+
						"Excess orders will be placed in a [Pending Provisioning] queue. "+
						"Are you sure you want to continue?",
					totalSlots, physAvail,
				)
				result.RequiresConfirmation = true

				// If not confirmed, return the warning without saving
				if !confirmed {
					return result, nil
				}
			}
		}
	}

	// Now apply the mutation and save
	if err := p.SetTotalSlots(totalSlots); err != nil {
		return nil, err
	}
	if err := s.repo.Save(p); err != nil {
		return nil, err
	}
	return result, nil
}

// SetRegion binds a product to a region (resource pool).
func (s *ProductAppService) SetRegion(id, regionID string) error {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	p.SetRegionID(regionID)
	return s.repo.Save(p)
}

// SetResourcePool binds a product to a resource pool.
func (s *ProductAppService) SetResourcePool(id, poolID string) error {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	p.SetResourcePoolID(poolID)
	return s.repo.Save(p)
}

// publishEvents sends all pending domain events from the aggregate to the bus.
func (s *ProductAppService) publishEvents(p *domain.Product) {
	if s.eventBus == nil {
		return
	}
	for _, e := range p.CollectEvents() {
		if evt, ok := e.(eventbus.Event); ok {
			s.eventBus.Publish(evt)
		}
	}
}
