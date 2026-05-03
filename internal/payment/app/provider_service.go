package app

import (
	"backend-core/internal/payment/domain"
	"fmt"
	"log"
	"sync"
	"time"
)

// ProviderFactory is a constructor function for a PaymentProvider.
// Infra packages register their factories at startup to avoid import cycles.
type ProviderFactory func(cfg *domain.PaymentProviderConfig, callback func(*domain.WebhookPayload)) domain.PaymentProvider

// NotifyURLBuilder builds the webhook callback URL for a given provider.
// Infra registers an implementation at startup so the app layer can
// derive the notify_url without importing infra.
type NotifyURLBuilder func(providerID string) (string, error)

// IDGen generates unique IDs. Reuses the same interface used by other contexts.
type IDGen interface {
	NewID() string
}

// ProviderAppService provides CRUD operations for payment provider configurations.
// Admins use this to add/configure payment providers; users query enabled providers.
type ProviderAppService struct {
	repo             domain.PaymentProviderRepo
	idGen            IDGen
	cache            sync.Map
	callback         func(*domain.WebhookPayload)
	factories        map[string]ProviderFactory
	notifyURLBuilder NotifyURLBuilder // optional — registered at startup
}

func NewProviderAppService(repo domain.PaymentProviderRepo, idGen IDGen) *ProviderAppService {
	return &ProviderAppService{
		repo:      repo,
		idGen:     idGen,
		cache:     sync.Map{},
		factories: make(map[string]ProviderFactory),
	}
}

// RegisterFactory registers a ProviderFactory for a given provider type.
// Called at startup (e.g. in main) by infra packages to avoid import cycles.
func (s *ProviderAppService) RegisterFactory(providerType string, factory ProviderFactory) {
	s.factories[providerType] = factory
}

// RegisterNotifyURLBuilder registers a function that builds webhook callback
// URLs. Called at startup so the app layer can derive runtime callback config
// without importing infra.
func (s *ProviderAppService) RegisterNotifyURLBuilder(builder NotifyURLBuilder) {
	s.notifyURLBuilder = builder
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

	// Validate and normalize provider-specific config before persistence.
	if err := s.prepareForStorage(p, true); err != nil {
		return nil, err
	}

	if err := s.repo.Create(p); err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}

	log.Printf("[ProviderAppService] provider created: id=%s type=%s name=%s", p.ID, p.Type, p.Name)
	return p, nil
}

// prepareForStorage normalizes provider config before persistence. EPay
// notify_url is derived at runtime, so stale values are stripped from storage.
func (s *ProviderAppService) prepareForStorage(p *domain.PaymentProviderConfig, requireWebhookURL bool) error {
	if p.Type != domain.ProviderTypeEPay {
		return nil
	}
	if p.Config == nil {
		p.Config = make(map[string]interface{})
	}
	delete(p.Config, "notify_url")
	// Default pay_type to "alipay" if not provided
	if existing, _ := p.Config["pay_type"].(string); existing == "" {
		p.Config["pay_type"] = "alipay"
	}
	if requireWebhookURL {
		return s.attachWebhookURL(p)
	}
	s.attachWebhookURLBestEffort(p)
	return nil
}

func (s *ProviderAppService) attachWebhookURL(p *domain.PaymentProviderConfig) error {
	if p.Type != domain.ProviderTypeEPay {
		return nil
	}
	if s.notifyURLBuilder == nil {
		return domain.ErrPublicBaseURLRequired
	}
	webhookURL, err := s.notifyURLBuilder(p.ID)
	if err != nil {
		return err
	}
	if webhookURL == "" {
		return domain.ErrPublicBaseURLRequired
	}
	p.WebhookURL = webhookURL
	return nil
}

func (s *ProviderAppService) attachWebhookURLBestEffort(p *domain.PaymentProviderConfig) {
	_ = s.attachWebhookURL(p)
}

func (s *ProviderAppService) runtimeConfig(p *domain.PaymentProviderConfig) (*domain.PaymentProviderConfig, error) {
	cfg := *p
	if p.Config != nil {
		cfg.Config = make(map[string]interface{}, len(p.Config)+1)
		for k, v := range p.Config {
			if k != "notify_url" {
				cfg.Config[k] = v
			}
		}
	} else {
		cfg.Config = make(map[string]interface{}, 1)
	}
	if cfg.Type == domain.ProviderTypeEPay {
		if err := s.attachWebhookURL(&cfg); err != nil {
			return nil, err
		}
		cfg.Config["notify_url"] = cfg.WebhookURL
	}
	return &cfg, nil
}

// GetProviderConfig returns a single provider by ID.
func (s *ProviderAppService) GetProviderConfig(id string) (*domain.PaymentProviderConfig, error) {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	_ = s.prepareForStorage(p, false)
	return p, nil
}

// ListAllProviders returns all providers (for admin).
func (s *ProviderAppService) ListAllProviders() ([]*domain.PaymentProviderConfig, error) {
	providers, err := s.repo.ListAll()
	if err != nil {
		return nil, err
	}
	for _, p := range providers {
		_ = s.prepareForStorage(p, false)
	}
	return providers, nil
}

// ListEnabledProviders returns only enabled providers (for user-facing display).
func (s *ProviderAppService) ListEnabledProviders() ([]*domain.PaymentProviderConfig, error) {
	providers, err := s.repo.ListEnabled()
	if err != nil {
		return nil, err
	}
	for _, p := range providers {
		_ = s.prepareForStorage(p, false)
	}
	return providers, nil
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
	if err := s.prepareForStorage(existing, true); err != nil {
		return nil, err
	}
	existing.UpdatedAt = time.Now()

	if err := s.repo.Update(existing); err != nil {
		return nil, fmt.Errorf("update provider: %w", err)
	}

	s.invalidateProviderCache(id)
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
	if err := s.prepareForStorage(p, true); err != nil {
		return err
	}
	p.UpdatedAt = time.Now()
	if err := s.repo.Update(p); err != nil {
		return err
	}
	s.invalidateProviderCache(id)
	return nil
}

// DisableProvider disables a provider.
func (s *ProviderAppService) DisableProvider(id string) error {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("provider not found: %w", err)
	}
	p.Enabled = false
	if err := s.prepareForStorage(p, false); err != nil {
		return err
	}
	p.UpdatedAt = time.Now()
	if err := s.repo.Update(p); err != nil {
		return err
	}
	s.invalidateProviderCache(id)
	return nil
}

// DeleteProvider removes a provider permanently.
func (s *ProviderAppService) DeleteProvider(id string) error {
	if err := s.repo.Delete(id); err != nil {
		return fmt.Errorf("delete provider: %w", err)
	}
	s.invalidateProviderCache(id)
	log.Printf("[ProviderAppService] provider deleted: id=%s", id)
	return nil
}

func (s *ProviderAppService) invalidateProviderCache(id string) {
	// Provider instances own parsed config, so mutations must force reconstruction.
	s.cache.Delete(id)
}

func (s *ProviderAppService) SetCallback(cb func(*domain.WebhookPayload)) {
	s.callback = cb
}

// GetProvider returns a live PaymentProvider instance for the given ID.
// Providers are cached after first construction. Factories must be registered
// via RegisterFactory before GetProvider is called for that type.
func (s *ProviderAppService) GetProvider(id string) (domain.PaymentProvider, error) {
	// Return cached instance if available
	if cached, ok := s.cache.Load(id); ok {
		return cached.(domain.PaymentProvider), nil
	}

	cfg, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("provider not found: %w", err)
	}
	runtimeCfg, err := s.runtimeConfig(cfg)
	if err != nil {
		return nil, err
	}

	factory, ok := s.factories[runtimeCfg.Type]
	if !ok {
		return nil, fmt.Errorf("no factory registered for provider type %q", runtimeCfg.Type)
	}

	prov := factory(runtimeCfg, s.callback)
	if runtimeCfg.Type == domain.ProviderTypeEPay {
		return prov, nil
	}
	s.cache.LoadOrStore(id, prov)
	return prov, nil
}
