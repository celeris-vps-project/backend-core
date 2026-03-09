package app

import (
	"backend-core/internal/product/domain"
	"fmt"
)

// GroupAppService implements the use-cases for the Group aggregate.
type GroupAppService struct {
	repo domain.GroupRepository
	ids  IDGenerator
}

func NewGroupAppService(repo domain.GroupRepository, ids IDGenerator) *GroupAppService {
	return &GroupAppService{repo: repo, ids: ids}
}

// CreateGroup creates a new product group / category.
func (s *GroupAppService) CreateGroup(name, description string, sortOrder int) (*domain.Group, error) {
	id := s.ids.NewID()
	g, err := domain.NewGroup(id, name, description, sortOrder)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Save(g); err != nil {
		return nil, err
	}
	return g, nil
}

// GetGroup returns a single group by ID.
func (s *GroupAppService) GetGroup(id string) (*domain.Group, error) {
	return s.repo.GetByID(id)
}

// ListGroups returns all groups ordered by sort_order.
func (s *GroupAppService) ListGroups() ([]*domain.Group, error) {
	return s.repo.ListAll()
}

// UpdateGroup modifies the mutable fields of an existing group.
func (s *GroupAppService) UpdateGroup(id, name, description string, sortOrder int) (*domain.Group, error) {
	g, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if name != "" {
		if err := g.SetName(name); err != nil {
			return nil, err
		}
	}
	g.SetDescription(description)
	g.SetSortOrder(sortOrder)
	if err := s.repo.Save(g); err != nil {
		return nil, err
	}
	return g, nil
}

// DeleteGroup removes a group by ID.
func (s *GroupAppService) DeleteGroup(id string) error {
	// Verify existence first
	if _, err := s.repo.GetByID(id); err != nil {
		return fmt.Errorf("app_error: group not found: %w", err)
	}
	return s.repo.Delete(id)
}
