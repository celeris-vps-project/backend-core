package infra

import (
	"backend-core/internal/node/domain"
	"backend-core/pkg/contracts"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ---- Persistence Objects ----

// HostNodePO stores persistent configuration and capacity data only.
// Runtime state (status, CPU/mem/disk usage, etc.) is NOT stored here —
// it lives in the NodeStateCache.
type HostNodePO struct {
	ID             string    `gorm:"primaryKey;column:id"`
	Code           string    `gorm:"uniqueIndex;column:code"`
	Location       string    `gorm:"index;column:location"`
	RegionID       string    `gorm:"index;column:region_id"`
	ResourcePoolID string    `gorm:"index;column:resource_pool_id"`
	Name           string    `gorm:"column:name"`
	Secret         string    `gorm:"column:secret"`
	NodeToken      string    `gorm:"column:node_token;index"`
	CreatedAt      time.Time `gorm:"column:created_at"`

	// Capacity fields (merged from the old NodePO / "nodes" table)
	TotalSlots int  `gorm:"column:total_slots;default:0"`
	UsedSlots  int  `gorm:"column:used_slots;default:0"`
	Enabled    bool `gorm:"column:enabled;default:true"`
}

func (HostNodePO) TableName() string { return "host_nodes" }

type IPAddressPO struct {
	ID         string `gorm:"primaryKey;column:id"`
	NodeID     string `gorm:"index;column:node_id"`
	Address    string `gorm:"column:address"`
	Version    int    `gorm:"column:version"`
	InstanceID string `gorm:"column:instance_id"`
}

func (IPAddressPO) TableName() string { return "ip_addresses" }

type TaskPO struct {
	ID         string `gorm:"primaryKey;column:id"`
	NodeID     string `gorm:"index;column:node_id"`
	Type       string `gorm:"column:type"`
	Status     string `gorm:"column:status"`
	SpecJSON   string `gorm:"column:spec_json;type:text"`
	Error      string `gorm:"column:error"`
	CreatedAt  string `gorm:"column:created_at"`
	FinishedAt string `gorm:"column:finished_at"`
}

func (TaskPO) TableName() string { return "tasks" }

// ---- HostNode Repository ----

type SqliteHostNodeRepo struct{ db *gorm.DB }

func NewSqliteHostNodeRepo(db *gorm.DB) *SqliteHostNodeRepo { return &SqliteHostNodeRepo{db: db} }

func (r *SqliteHostNodeRepo) GetByID(id string) (*domain.HostNode, error) {
	var po HostNodePO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("host node not found")
		}
		return nil, err
	}
	return hostToDomain(po), nil
}

func (r *SqliteHostNodeRepo) GetByCode(code string) (*domain.HostNode, error) {
	var po HostNodePO
	if err := r.db.Where("code = ?", code).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("host node not found")
		}
		return nil, err
	}
	return hostToDomain(po), nil
}

func (r *SqliteHostNodeRepo) GetByNodeToken(token string) (*domain.HostNode, error) {
	var po HostNodePO
	if err := r.db.Where("node_token = ? AND node_token != ''", token).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("host node not found")
		}
		return nil, err
	}
	return hostToDomain(po), nil
}

func (r *SqliteHostNodeRepo) ListAll() ([]*domain.HostNode, error) {
	var pos []HostNodePO
	if err := r.db.Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.HostNode, len(pos))
	for i, po := range pos {
		out[i] = hostToDomain(po)
	}
	return out, nil
}

func (r *SqliteHostNodeRepo) ListByLocation(loc string) ([]*domain.HostNode, error) {
	var pos []HostNodePO
	if err := r.db.Where("location = ?", loc).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.HostNode, len(pos))
	for i, po := range pos {
		out[i] = hostToDomain(po)
	}
	return out, nil
}

func (r *SqliteHostNodeRepo) ListByRegionID(regionID string) ([]*domain.HostNode, error) {
	var pos []HostNodePO
	if err := r.db.Where("region_id = ?", regionID).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.HostNode, len(pos))
	for i, po := range pos {
		out[i] = hostToDomain(po)
	}
	return out, nil
}

func (r *SqliteHostNodeRepo) ListEnabledByRegionID(regionID string) ([]*domain.HostNode, error) {
	var pos []HostNodePO
	if err := r.db.Where("region_id = ? AND enabled = ?", regionID, true).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.HostNode, len(pos))
	for i, po := range pos {
		out[i] = hostToDomain(po)
	}
	return out, nil
}

func (r *SqliteHostNodeRepo) ListByResourcePoolID(poolID string) ([]*domain.HostNode, error) {
	var pos []HostNodePO
	if err := r.db.Where("resource_pool_id = ?", poolID).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.HostNode, len(pos))
	for i, po := range pos {
		out[i] = hostToDomain(po)
	}
	return out, nil
}

func (r *SqliteHostNodeRepo) ListEnabledByResourcePoolID(poolID string) ([]*domain.HostNode, error) {
	var pos []HostNodePO
	if err := r.db.Where("resource_pool_id = ? AND enabled = ?", poolID, true).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.HostNode, len(pos))
	for i, po := range pos {
		out[i] = hostToDomain(po)
	}
	return out, nil
}

func (r *SqliteHostNodeRepo) Save(n *domain.HostNode) error {
	po := hostFromDomain(n)
	return r.db.Save(&po).Error
}

// ---- IPAddress Repository ----

type SqliteIPAddressRepo struct{ db *gorm.DB }

func NewSqliteIPAddressRepo(db *gorm.DB) *SqliteIPAddressRepo { return &SqliteIPAddressRepo{db: db} }

func (r *SqliteIPAddressRepo) GetByID(id string) (*domain.IPAddress, error) {
	var po IPAddressPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("ip not found")
		}
		return nil, err
	}
	return ipToDomain(po), nil
}

func (r *SqliteIPAddressRepo) ListByNodeID(nodeID string) ([]*domain.IPAddress, error) {
	var pos []IPAddressPO
	if err := r.db.Where("node_id = ?", nodeID).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.IPAddress, len(pos))
	for i, po := range pos {
		out[i] = ipToDomain(po)
	}
	return out, nil
}

func (r *SqliteIPAddressRepo) FindAvailable(nodeID string, version int) (*domain.IPAddress, error) {
	var po IPAddressPO
	err := r.db.Where("node_id = ? AND version = ? AND instance_id = ''", nodeID, version).First(&po).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("no available ip")
		}
		return nil, err
	}
	return ipToDomain(po), nil
}

func (r *SqliteIPAddressRepo) Save(ip *domain.IPAddress) error {
	po := ipFromDomain(ip)
	return r.db.Save(&po).Error
}

// ---- Task Repository ----

type SqliteTaskRepo struct{ db *gorm.DB }

func NewSqliteTaskRepo(db *gorm.DB) *SqliteTaskRepo { return &SqliteTaskRepo{db: db} }

func (r *SqliteTaskRepo) GetByID(id string) (*contracts.Task, error) {
	var po TaskPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("task not found")
		}
		return nil, err
	}
	t := taskToDomain(po)
	return &t, nil
}

func (r *SqliteTaskRepo) ListPendingByNodeID(nodeID string) ([]contracts.Task, error) {
	var pos []TaskPO
	if err := r.db.Where("node_id = ? AND status = ?", nodeID, string(contracts.TaskStatusQueued)).Find(&pos).Error; err != nil {
		return nil, err
	}
	out := make([]contracts.Task, len(pos))
	for i, po := range pos {
		out[i] = taskToDomain(po)
	}
	return out, nil
}

func (r *SqliteTaskRepo) Save(t *contracts.Task) error {
	po := taskFromDomain(*t)
	return r.db.Save(&po).Error
}

// ---- Mapping helpers ----

func hostToDomain(po HostNodePO) *domain.HostNode {
	return domain.ReconstituteHostNode(
		po.ID, po.Code, po.Location, po.RegionID, po.ResourcePoolID, po.Name, po.Secret, po.NodeToken,
		po.CreatedAt,
		po.TotalSlots, po.UsedSlots, po.Enabled,
	)
}

func hostFromDomain(n *domain.HostNode) HostNodePO {
	return HostNodePO{
		ID: n.ID(), Code: n.Code(), Location: n.Location(), RegionID: n.RegionID(),
		ResourcePoolID: n.ResourcePoolID(), Name: n.Name(), Secret: n.Secret(),
		NodeToken:  n.NodeToken(),
		CreatedAt:  n.CreatedAt(),
		TotalSlots: n.TotalSlots(), UsedSlots: n.UsedSlots(), Enabled: n.Enabled(),
	}
}

func ipToDomain(po IPAddressPO) *domain.IPAddress {
	return domain.ReconstituteIPAddress(po.ID, po.NodeID, po.Address, po.Version, po.InstanceID)
}

func ipFromDomain(ip *domain.IPAddress) IPAddressPO {
	return IPAddressPO{ID: ip.ID(), NodeID: ip.NodeID(), Address: ip.Address(), Version: ip.Version(), InstanceID: ip.InstanceID()}
}

func taskToDomain(po TaskPO) contracts.Task {
	return contracts.Task{
		ID: po.ID, NodeID: po.NodeID,
		Type: contracts.TaskType(po.Type), Status: contracts.TaskStatus(po.Status),
		Error: po.Error, CreatedAt: po.CreatedAt, FinishedAt: po.FinishedAt,
		// Note: Spec deserialization from SpecJSON would go here in production
	}
}

func taskFromDomain(t contracts.Task) TaskPO {
	return TaskPO{
		ID: t.ID, NodeID: t.NodeID,
		Type: string(t.Type), Status: string(t.Status),
		Error: t.Error, CreatedAt: t.CreatedAt, FinishedAt: t.FinishedAt,
		// Note: Spec serialization to SpecJSON would go here in production
	}
}
