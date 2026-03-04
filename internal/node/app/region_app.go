package app

import "backend-core/internal/node/domain"

type RegionAppService struct {
	regionRepo domain.RegionRepository
	ids        IDGenerator
}

func NewRegionAppService(regionRepo domain.RegionRepository, ids IDGenerator) *RegionAppService {
	return &RegionAppService{regionRepo: regionRepo, ids: ids}
}

// ---- Region CRUD ----

func (s *RegionAppService) CreateRegion(code, name, flagIcon string) (*domain.Region, error) {
	id := s.ids.NewID()
	r, err := domain.NewRegion(id, code, name, flagIcon)
	if err != nil {
		return nil, err
	}
	if err := s.regionRepo.Save(r); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *RegionAppService) GetRegion(id string) (*domain.Region, error) {
	return s.regionRepo.GetByID(id)
}

func (s *RegionAppService) ListAll() ([]*domain.Region, error) {
	return s.regionRepo.ListAll()
}

func (s *RegionAppService) ListActive() ([]*domain.Region, error) {
	return s.regionRepo.ListActive()
}

func (s *RegionAppService) Activate(id string) error {
	r, err := s.regionRepo.GetByID(id)
	if err != nil {
		return err
	}
	r.Activate()
	return s.regionRepo.Save(r)
}

func (s *RegionAppService) Deactivate(id string) error {
	r, err := s.regionRepo.GetByID(id)
	if err != nil {
		return err
	}
	r.Deactivate()
	return s.regionRepo.Save(r)
}
