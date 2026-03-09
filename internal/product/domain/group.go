package domain

import (
	"errors"
	"time"
)

// Group represents a product group / category used to organise products
// for display to customers (e.g. "VPS Plans", "Dedicated Servers").
type Group struct {
	id          string
	name        string
	description string
	sortOrder   int
	createdAt   time.Time
	updatedAt   time.Time
}

// NewGroup creates a brand-new Group aggregate with validation.
func NewGroup(id, name, description string, sortOrder int) (*Group, error) {
	if id == "" {
		return nil, errors.New("domain_error: group id is required")
	}
	if name == "" {
		return nil, errors.New("domain_error: group name is required")
	}
	now := time.Now()
	return &Group{
		id:          id,
		name:        name,
		description: description,
		sortOrder:   sortOrder,
		createdAt:   now,
		updatedAt:   now,
	}, nil
}

// ReconstituteGroup re-hydrates a Group from persistence (no validation).
func ReconstituteGroup(id, name, description string, sortOrder int, createdAt, updatedAt time.Time) *Group {
	return &Group{
		id:          id,
		name:        name,
		description: description,
		sortOrder:   sortOrder,
		createdAt:   createdAt,
		updatedAt:   updatedAt,
	}
}

// ---- Getters ----

func (g *Group) ID() string           { return g.id }
func (g *Group) Name() string         { return g.name }
func (g *Group) Description() string  { return g.description }
func (g *Group) SortOrder() int       { return g.sortOrder }
func (g *Group) CreatedAt() time.Time { return g.createdAt }
func (g *Group) UpdatedAt() time.Time { return g.updatedAt }

// ---- Mutators ----

func (g *Group) SetName(name string) error {
	if name == "" {
		return errors.New("domain_error: group name is required")
	}
	g.name = name
	g.updatedAt = time.Now()
	return nil
}

func (g *Group) SetDescription(desc string) {
	g.description = desc
	g.updatedAt = time.Now()
}

func (g *Group) SetSortOrder(n int) {
	g.sortOrder = n
	g.updatedAt = time.Now()
}

// GroupRepository defines the persistence contract for the Group aggregate.
type GroupRepository interface {
	GetByID(id string) (*Group, error)
	ListAll() ([]*Group, error)
	Save(group *Group) error
	Delete(id string) error
}
