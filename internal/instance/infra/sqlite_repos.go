package infra

import (
	"backend-core/internal/instance/domain"
	nodeDomain "backend-core/internal/node/domain"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ---- HostNodeAllocatorAdapter ----
// Adapts node/domain.HostNodeRepository to satisfy instance/domain.NodeAllocatorRepository.
// This is the anti-corruption layer between the two bounded contexts.

type HostNodeAllocatorAdapter struct {
	hostRepo nodeDomain.HostNodeRepository
}

func NewHostNodeAllocatorAdapter(hostRepo nodeDomain.HostNodeRepository) *HostNodeAllocatorAdapter {
	return &HostNodeAllocatorAdapter{hostRepo: hostRepo}
}

func (a *HostNodeAllocatorAdapter) GetByID(id string) (domain.NodeAllocator, error) {
	return a.hostRepo.GetByID(id)
}

func (a *HostNodeAllocatorAdapter) ListAll() ([]domain.NodeAllocator, error) {
	nodes, err := a.hostRepo.ListAll()
	if err != nil {
		return nil, err
	}
	out := make([]domain.NodeAllocator, len(nodes))
	for i, n := range nodes {
		out[i] = n
	}
	return out, nil
}

func (a *HostNodeAllocatorAdapter) ListByLocation(location string) ([]domain.NodeAllocator, error) {
	nodes, err := a.hostRepo.ListByLocation(location)
	if err != nil {
		return nil, err
	}
	out := make([]domain.NodeAllocator, len(nodes))
	for i, n := range nodes {
		out[i] = n
	}
	return out, nil
}

func (a *HostNodeAllocatorAdapter) Save(node domain.NodeAllocator) error {
	hn, ok := node.(*nodeDomain.HostNode)
	if !ok {
		return errors.New("infra_error: expected *HostNode")
	}
	return a.hostRepo.Save(hn)
}

// ---- Persistence Objects ----

type InstancePO struct {
	ID           string     `gorm:"primaryKey;column:id"`
	CustomerID   string     `gorm:"index;column:customer_id"`
	OrderID      string     `gorm:"index;column:order_id"`
	NodeID       string     `gorm:"index;column:node_id"`
	Hostname     string     `gorm:"column:hostname"`
	Plan         string     `gorm:"column:plan"`
	OS           string     `gorm:"column:os"`
	CPU          int        `gorm:"column:cpu"`
	MemoryMB     int        `gorm:"column:memory_mb"`
	DiskGB       int        `gorm:"column:disk_gb"`
	IPv4         string     `gorm:"column:ipv4"`
	IPv6         string     `gorm:"column:ipv6"`
	Status       string     `gorm:"column:status"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	StartedAt    *time.Time `gorm:"column:started_at"`
	StoppedAt    *time.Time `gorm:"column:stopped_at"`
	SuspendedAt  *time.Time `gorm:"column:suspended_at"`
	TerminatedAt *time.Time `gorm:"column:terminated_at"`
}

func (InstancePO) TableName() string { return "instances" }

// ---- Instance Repository ----

type SqliteInstanceRepo struct{ db *gorm.DB }

func NewSqliteInstanceRepo(db *gorm.DB) *SqliteInstanceRepo { return &SqliteInstanceRepo{db: db} }

func (r *SqliteInstanceRepo) GetByID(id string) (*domain.Instance, error) {
	var po InstancePO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("instance not found")
		}
		return nil, err
	}
	return instanceToDomain(po), nil
}

func (r *SqliteInstanceRepo) ListByCustomerID(customerID string) ([]*domain.Instance, error) {
	var pos []InstancePO
	if err := r.db.Where("customer_id = ?", customerID).Find(&pos).Error; err != nil {
		return nil, err
	}
	insts := make([]*domain.Instance, len(pos))
	for i, po := range pos {
		insts[i] = instanceToDomain(po)
	}
	return insts, nil
}

func (r *SqliteInstanceRepo) ListByNodeID(nodeID string) ([]*domain.Instance, error) {
	var pos []InstancePO
	if err := r.db.Where("node_id = ?", nodeID).Find(&pos).Error; err != nil {
		return nil, err
	}
	insts := make([]*domain.Instance, len(pos))
	for i, po := range pos {
		insts[i] = instanceToDomain(po)
	}
	return insts, nil
}

func (r *SqliteInstanceRepo) Save(inst *domain.Instance) error {
	po := instanceFromDomain(inst)
	return r.db.Save(&po).Error
}

// ---- Mapping ----

func instanceToDomain(po InstancePO) *domain.Instance {
	return domain.ReconstituteInstance(
		po.ID, po.CustomerID, po.OrderID, po.NodeID,
		po.Hostname, po.Plan, po.OS,
		po.CPU, po.MemoryMB, po.DiskGB,
		po.IPv4, po.IPv6, po.Status,
		po.CreatedAt,
		po.StartedAt, po.StoppedAt, po.SuspendedAt, po.TerminatedAt,
	)
}

func instanceFromDomain(i *domain.Instance) InstancePO {
	return InstancePO{
		ID: i.ID(), CustomerID: i.CustomerID(), OrderID: i.OrderID(), NodeID: i.NodeID(),
		Hostname: i.Hostname(), Plan: i.Plan(), OS: i.OS(),
		CPU: i.CPU(), MemoryMB: i.MemoryMB(), DiskGB: i.DiskGB(),
		IPv4: i.IPv4(), IPv6: i.IPv6(), Status: i.Status(),
		CreatedAt: i.CreatedAt(),
		StartedAt: i.StartedAt(), StoppedAt: i.StoppedAt(),
		SuspendedAt: i.SuspendedAt(), TerminatedAt: i.TerminatedAt(),
	}
}
