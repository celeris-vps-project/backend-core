package app

import (
	"backend-core/internal/catalog/domain"
	"backend-core/pkg/eventbus"
	"backend-core/pkg/events"
	"context"
	"fmt"
	"strings"
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
func (s *ProductAppService) CreateProduct(ctx context.Context, name, slug, location, regionID, resourcePoolID, networkMode string, cpu, memoryMB, diskGB, bandwidthGB int, priceAmount int64, currency string, cycle domain.BillingCycle, totalSlots int) (*domain.Product, error) {
	id := s.ids.NewID()
	p, err := domain.NewProduct(id, name, slug, location, cpu, memoryMB, diskGB, bandwidthGB, priceAmount, currency, cycle, totalSlots)
	if err != nil {
		return nil, err
	}
	mode, err := normalizeNetworkMode(networkMode)
	if err != nil {
		return nil, err
	}
	if regionID != "" {
		p.SetRegionID(regionID)
	}
	if resourcePoolID != "" {
		p.SetResourcePoolID(resourcePoolID)
	}
	p.SetNetworkMode(mode)
	if err := s.repo.Save(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// PurchaseProduct processes a customer purchase: consumes a commercial slot
// and publishes a ProductPurchasedEvent for the Node domain to handle
// physical provisioning independently.
func (s *ProductAppService) PurchaseProduct(
	ctx context.Context, productID, customerID, orderID, instanceID, hostname, os string,
) (*domain.Product, error) {
	p, err := s.repo.GetByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if !p.Enabled() {
		return nil, fmt.Errorf("app_error: product %s is not available", productID)
	}

	// Use atomic database-level slot consumption to prevent the
	// read-modify-write race condition under concurrent purchases.
	if err := s.repo.ConsumeSlotAtomic(ctx, productID); err != nil {
		return nil, err
	}

	// Reload the product to get the updated sold_slots count for the response.
	p, err = s.repo.GetByID(ctx, productID)
	if err != nil {
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
		InstanceID:     instanceID,
		Hostname:       hostname,
		OS:             os,
		CPU:            p.CPU(),
		MemoryMB:       p.MemoryMB(),
		DiskGB:         p.DiskGB(),
		NetworkMode:    p.NetworkMode(),
	})

	// Publish collected domain events
	s.publishEvents(p)
	return p, nil
}

func (s *ProductAppService) GetProduct(ctx context.Context, id string) (*domain.Product, error) {
	return s.repo.GetByID(ctx, id)
}
func (s *ProductAppService) GetBySlug(ctx context.Context, slug string) (*domain.Product, error) {
	return s.repo.GetBySlug(ctx, slug)
}
func (s *ProductAppService) ListAll(ctx context.Context) ([]*domain.Product, error) {
	return s.repo.ListAll(ctx)
}
func (s *ProductAppService) ListEnabled(ctx context.Context) ([]*domain.Product, error) {
	return s.repo.ListEnabled(ctx)
}

func (s *ProductAppService) EnableProduct(ctx context.Context, id string) error {
	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	p.Enable()
	return s.repo.Save(ctx, p)
}

func (s *ProductAppService) DisableProduct(ctx context.Context, id string) error {
	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	p.Disable()
	return s.repo.Save(ctx, p)
}

func (s *ProductAppService) UpdatePrice(ctx context.Context, id string, amount int64, currency string) error {
	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := p.SetPrice(amount, currency); err != nil {
		return err
	}
	return s.repo.Save(ctx, p)
}

func (s *ProductAppService) UpdateNetworkMode(ctx context.Context, id, mode string) error {
	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	normalized, err := normalizeNetworkMode(mode)
	if err != nil {
		return err
	}
	p.SetNetworkMode(normalized)
	return s.repo.Save(ctx, p)
}

// AdjustStock sets the commercial inventory slots for a product (admin restocking).
func (s *ProductAppService) AdjustStock(ctx context.Context, id string, totalSlots int, confirmed bool) (*RestockResult, error) {
	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if totalSlots < domain.UnlimitedSlots {
		return nil, fmt.Errorf("domain_error: total slots must be >= -1 (-1 = unlimited)")
	}
	if totalSlots != domain.UnlimitedSlots && totalSlots < p.SoldSlots() {
		return nil, fmt.Errorf("domain_error: total slots cannot be less than sold slots")
	}

	result := &RestockResult{Product: p}

	// Check physical capacity if a capacity checker is available and product has a region
	if s.capacityChecker != nil && p.RegionID() != "" && totalSlots != domain.UnlimitedSlots {
		checkID := p.ResourcePoolID()
		if checkID == "" {
			checkID = p.RegionID()
		}
		physAvail, err := s.capacityChecker.AvailablePhysicalSlots(ctx, checkID)
		if err == nil {
			result.PhysicalAvailable = physAvail

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

				if !confirmed {
					return result, nil
				}
			}
		}
	}

	if err := p.SetTotalSlots(totalSlots); err != nil {
		return nil, err
	}
	if err := s.repo.Save(ctx, p); err != nil {
		return nil, err
	}
	return result, nil
}

// SetRegion binds a product to a region (resource pool).
func (s *ProductAppService) SetRegion(ctx context.Context, id, regionID string) error {
	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	p.SetRegionID(regionID)
	return s.repo.Save(ctx, p)
}

// SetResourcePool binds a product to a resource pool.
func (s *ProductAppService) SetResourcePool(ctx context.Context, id, poolID string) error {
	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	p.SetResourcePoolID(poolID)
	return s.repo.Save(ctx, p)
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

func normalizeNetworkMode(mode string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(mode)); normalized {
	case "", "dedicated":
		return "dedicated", nil
	case "nat":
		return "nat", nil
	default:
		return "", fmt.Errorf("domain_error: invalid network mode %q", mode)
	}
}
