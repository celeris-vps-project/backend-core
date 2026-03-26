package app

import (
	"backend-core/internal/payment/domain"
	"fmt"
	"log"
	"time"
)

// IDGen generates unique IDs. Reuses the same interface used by other contexts.
type IDGen interface {
	NewID() string
}

// ProviderAppService provides CRUD operations for payment provider configurations.
// Admins use this to add/configure payment providers; users query enabled providers.
type ProviderAppService struct {
	repo  domain.PaymentProviderRepo
	idGen IDGen
}

func NewProviderAppService(repo domain.PaymentProviderRepo, idGen IDGen) *ProviderAppService {
	return &ProviderAppService{repo: repo, idGen: idGen}
}

// CreateProvider creates a new payment provider configuration.
func (s *ProviderAppService) CreateProvider(providerType, name string, sortOrder int, config map[string]interface{}) (*domain.PaymentProviderConfig, error) {
	if providerType == "" {
		return nil, fmt.Errorf("provider type is required")
	}
	if name == "" {
		return nil, fmt.Errorf("provider name is required")
	}

	now := time.Now()
	p := &domain.PaymentProviderConfig{
		ID:        s.idGen.NewID(),
		Type:      providerType,
		Name:      name,
		Enabled:   true,
		SortOrder: sortOrder,
		Config:    config,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.Create(p); err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}

	log.Printf("[ProviderAppService] provider created: id=%s type=%s name=%s", p.ID, p.Type, p.Name)
	return p, nil
}

// GetProvider returns a single provider by ID.
func (s *ProviderAppService) GetProvider(id string) (*domain.PaymentProviderConfig, error) {
	return s.repo.GetByID(id)
}

// ListAllProviders returns all providers (for admin).
func (s *ProviderAppService) ListAllProviders() ([]*domain.PaymentProviderConfig, error) {
	return s.repo.ListAll()
}

// ListEnabledProviders returns only enabled providers (for user-facing display).
func (s *ProviderAppService) ListEnabledProviders() ([]*domain.PaymentProviderConfig, error) {
	return s.repo.ListEnabled()
}

// UpdateProvider updates a provider's configuration.
func (s *ProviderAppService) UpdateProvider(id, name string, sortOrder int, config map[string]interface{}) (*domain.PaymentProviderConfig, error) {
	existing, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("provider not found: %w", err)
	}

	if name != "" {
		existing.Name = name
	}
	existing.SortOrder = sortOrder
	if config != nil {
		existing.Config = config
	}
	existing.UpdatedAt = time.Now()

	if err := s.repo.Update(existing); err != nil {
		return nil, fmt.Errorf("update provider: %w", err)
	}

	log.Printf("[ProviderAppService] provider updated: id=%s name=%s", id, existing.Name)
	return existing, nil
}

// EnableProvider enables a provider.
func (s *ProviderAppService) EnableProvider(id string) error {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("provider not found: %w", err)
	}
	p.Enabled = true
	p.UpdatedAt = time.Now()
	return s.repo.Update(p)
}

// DisableProvider disables a provider.
func (s *ProviderAppService) DisableProvider(id string) error {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("provider not found: %w", err)
	}
	p.Enabled = false
	p.UpdatedAt = time.Now()
	return s.repo.Update(p)
}

// DeleteProvider removes a provider permanently.
func (s *ProviderAppService) DeleteProvider(id string) error {
	if err := s.repo.Delete(id); err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	log.Printf("[ProviderAppService] provider deleted: id=%s", id)
	return nil
}
