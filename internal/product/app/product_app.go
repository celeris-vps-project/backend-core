package app

import "backend-core/internal/product/domain"

type IDGenerator interface{ NewID() string }

type ProductAppService struct {
	repo domain.ProductRepository
	ids  IDGenerator
}

func NewProductAppService(repo domain.ProductRepository, ids IDGenerator) *ProductAppService {
	return &ProductAppService{repo: repo, ids: ids}
}

func (s *ProductAppService) CreateProduct(name, slug string, cpu, memoryMB, diskGB, bandwidthGB int, priceAmount int64, currency string, cycle domain.BillingCycle) (*domain.Product, error) {
	id := s.ids.NewID()
	p, err := domain.NewProduct(id, name, slug, cpu, memoryMB, diskGB, bandwidthGB, priceAmount, currency, cycle)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Save(p); err != nil {
		return nil, err
	}
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
