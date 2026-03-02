package infra

import (
	"backend-core/internal/instance/domain"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ---- Persistence Objects ----

type NodePO struct {
	ID         string `gorm:"primaryKey;column:id"`
	Code       string `gorm:"uniqueIndex;column:code"`
	Location   string `gorm:"index;column:location"`
	Name       string `gorm:"column:name"`
	TotalSlots int    `gorm:"column:total_slots"`
	UsedSlots  int    `gorm:"column:used_slots"`
	Enabled    bool   `gorm:"column:enabled"`
}

func (NodePO) TableName() string { return "nodes" }

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

// ---- Node Repository ----

type SqliteNodeRepo struct{ db *gorm.DB }

func NewSqliteNodeRepo(db *gorm.DB) *SqliteNodeRepo { return &SqliteNodeRepo{db: db} }

func (r *SqliteNodeRepo) GetByID(id string) (*domain.Node, error) {
	var po NodePO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("node not found")
		}
		return nil, err
	}
	return nodeToDomain(po), nil
}

func (r *SqliteNodeRepo) ListAll() ([]*domain.Node, error) {
	var pos []NodePO
	if err := r.db.Find(&pos).Error; err != nil {
		return nil, err
	}
	nodes := make([]*domain.Node, len(pos))
	for i, po := range pos {
		nodes[i] = nodeToDomain(po)
	}
	return nodes, nil
}

func (r *SqliteNodeRepo) ListByLocation(location string) ([]*domain.Node, error) {
	var pos []NodePO
	if err := r.db.Where("location = ?", location).Find(&pos).Error; err != nil {
		return nil, err
	}
	nodes := make([]*domain.Node, len(pos))
	for i, po := range pos {
		nodes[i] = nodeToDomain(po)
	}
	return nodes, nil
}

func (r *SqliteNodeRepo) Save(node *domain.Node) error {
	po := nodeFromDomain(node)
	return r.db.Save(&po).Error
}

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

func nodeToDomain(po NodePO) *domain.Node {
	return domain.ReconstituteNode(po.ID, po.Code, po.Location, po.Name, po.TotalSlots, po.UsedSlots, po.Enabled)
}

func nodeFromDomain(n *domain.Node) NodePO {
	return NodePO{
		ID: n.ID(), Code: n.Code(), Location: n.Location(), Name: n.Name(),
		TotalSlots: n.TotalSlots(), UsedSlots: n.UsedSlots(), Enabled: n.Enabled(),
	}
}

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
