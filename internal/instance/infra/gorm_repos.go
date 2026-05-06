package infra

import (
	"backend-core/internal/instance/domain"
	nodeDomain "backend-core/internal/provisioning/domain"
	"backend-core/pkg/database"
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

// wrappedNode couples a HostNode to its repository for saving.
// Nodes returned by this adapter are always *wrappedNode so that Save()
// can avoid a brittle concrete type assertion on *nodeDomain.HostNode.
// The wrapper is an internal infra detail — callers only see domain.NodeAllocator.
type wrappedNode struct {
	*nodeDomain.HostNode                               // promotes all NodeAllocator methods
	repo                 nodeDomain.HostNodeRepository // back-reference for persisting
}

func (w *wrappedNode) save() error {
	return w.repo.Save(w.HostNode)
}

func (a *HostNodeAllocatorAdapter) wrap(hn *nodeDomain.HostNode) *wrappedNode {
	return &wrappedNode{HostNode: hn, repo: a.hostRepo}
}

func (a *HostNodeAllocatorAdapter) GetByID(id string) (domain.NodeAllocator, error) {
	hn, err := a.hostRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	return a.wrap(hn), nil
}

func (a *HostNodeAllocatorAdapter) ListAll() ([]domain.NodeAllocator, error) {
	nodes, err := a.hostRepo.ListAll()
	if err != nil {
		return nil, err
	}
	out := make([]domain.NodeAllocator, len(nodes))
	for i, n := range nodes {
		out[i] = a.wrap(n)
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
		out[i] = a.wrap(n)
	}
	return out, nil
}

func (a *HostNodeAllocatorAdapter) AllocateSlotAtomic(nodeID string) error {
	return a.hostRepo.AllocateSlotAtomic(nodeID)
}

func (a *HostNodeAllocatorAdapter) ReleaseSlotAtomic(nodeID string) error {
	return a.hostRepo.ReleaseSlotAtomic(nodeID)
}

// Save persists mutations (e.g. AllocateSlot / ReleaseSlot) back to the host repository.
// It type-asserts to *wrappedNode (an internal infra type) rather than the concrete
// provisioning domain type, eliminating the cross-context type assertion.
func (a *HostNodeAllocatorAdapter) Save(node domain.NodeAllocator) error {
	wn, ok := node.(*wrappedNode)
	if !ok {
		return errors.New("infra_error: node was not returned by HostNodeAllocatorAdapter")
	}
	return wn.save()
}

// ---- Persistence Objects ----

type InstancePO struct {
	ID              string     `gorm:"primaryKey;column:id"`
	CustomerID      string     `gorm:"index;column:customer_id"`
	OrderID         string     `gorm:"index;column:order_id"`
	NodeID          string     `gorm:"index;column:node_id"`
	Hostname        string     `gorm:"column:hostname"`
	Plan            string     `gorm:"column:plan"`
	OS              string     `gorm:"column:os"`
	CPU             int        `gorm:"column:cpu"`
	MemoryMB        int        `gorm:"column:memory_mb"`
	DiskGB          int        `gorm:"column:disk_gb"`
	BandwidthGB     int        `gorm:"column:bandwidth_gb"`
	IPv4            string     `gorm:"column:ipv4"`
	IPv6            string     `gorm:"column:ipv6"`
	HostIP          string     `gorm:"column:host_ip"`
	Status          string     `gorm:"column:status"`
	SuspendReason   string     `gorm:"column:suspend_reason"`
	InitialPassword string     `gorm:"column:initial_password"`
	NetworkMode     string     `gorm:"column:network_mode;default:dedicated"` // "dedicated" or "nat"
	NATPort         int        `gorm:"column:nat_port;default:0"`             // NAT SSH port
	CreatedAt       time.Time  `gorm:"column:created_at"`
	StartedAt       *time.Time `gorm:"column:started_at"`
	StoppedAt       *time.Time `gorm:"column:stopped_at"`
	SuspendedAt     *time.Time `gorm:"column:suspended_at"`
	TerminatedAt    *time.Time `gorm:"column:terminated_at"`
}

func (InstancePO) TableName() string { return "instances" }

// ---- Instance Repository ----

// GormInstanceRepo implements domain.InstanceRepository using GORM.
// It is driver-agnostic: works with SQLite, PostgreSQL, or any GORM-supported database.
type GormInstanceRepo struct{ db *gorm.DB }

func NewGormInstanceRepo(db *gorm.DB) *GormInstanceRepo { return &GormInstanceRepo{db: db} }

func (r *GormInstanceRepo) GetByID(id string) (*domain.Instance, error) {
	var po InstancePO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("instance not found")
		}
		return nil, err
	}
	return instanceToDomain(po), nil
}

func (r *GormInstanceRepo) GetByOrderID(orderID string) (*domain.Instance, error) {
	var po InstancePO
	if err := r.db.Where("order_id = ?", orderID).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("instance not found")
		}
		return nil, err
	}
	return instanceToDomain(po), nil
}

func (r *GormInstanceRepo) ListAll() ([]*domain.Instance, error) {
	var pos []InstancePO
	if err := r.db.Find(&pos).Error; err != nil {
		return nil, err
	}
	insts := make([]*domain.Instance, len(pos))
	for i, po := range pos {
		insts[i] = instanceToDomain(po)
	}
	return insts, nil
}

func (r *GormInstanceRepo) ListByCustomerID(customerID string) ([]*domain.Instance, error) {
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

func (r *GormInstanceRepo) ListByNodeID(nodeID string) ([]*domain.Instance, error) {
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

func (r *GormInstanceRepo) Save(inst *domain.Instance) error {
	po := instanceFromDomain(inst)
	return database.UpsertByPrimaryKey(r.db, &po, instanceUpsertColumns)
}

// ---- Mapping ----

var instanceUpsertColumns = []string{
	"customer_id",
	"order_id",
	"node_id",
	"hostname",
	"plan",
	"os",
	"cpu",
	"memory_mb",
	"disk_gb",
	"bandwidth_gb",
	"ipv4",
	"ipv6",
	"host_ip",
	"status",
	"suspend_reason",
	"initial_password",
	"network_mode",
	"nat_port",
	"created_at",
	"started_at",
	"stopped_at",
	"suspended_at",
	"terminated_at",
}

func instanceToDomain(po InstancePO) *domain.Instance {
	return domain.ReconstituteInstanceFull(
		po.ID, po.CustomerID, po.OrderID, po.NodeID,
		po.Hostname, po.Plan, po.OS,
		po.CPU, po.MemoryMB, po.DiskGB, po.BandwidthGB,
		po.IPv4, po.IPv6, po.HostIP, po.Status, po.SuspendReason, po.InitialPassword,
		po.NetworkMode, po.NATPort,
		po.CreatedAt,
		po.StartedAt, po.StoppedAt, po.SuspendedAt, po.TerminatedAt,
	)
}

func instanceFromDomain(i *domain.Instance) InstancePO {
	return InstancePO{
		ID: i.ID(), CustomerID: i.CustomerID(), OrderID: i.OrderID(), NodeID: i.NodeID(),
		Hostname: i.Hostname(), Plan: i.Plan(), OS: i.OS(),
		CPU: i.CPU(), MemoryMB: i.MemoryMB(), DiskGB: i.DiskGB(), BandwidthGB: i.BandwidthGB(),
		IPv4: i.IPv4(), IPv6: i.IPv6(), HostIP: i.HostIP(), Status: i.Status(),
		SuspendReason: i.SuspendReason(), InitialPassword: i.InitialPassword(), NetworkMode: i.NetworkMode(), NATPort: i.NATPort(),
		CreatedAt: i.CreatedAt(),
		StartedAt: i.StartedAt(), StoppedAt: i.StoppedAt(),
		SuspendedAt: i.SuspendedAt(), TerminatedAt: i.TerminatedAt(),
	}
}
