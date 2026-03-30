package infra

import (
	"backend-core/internal/provisioning/domain"
	"backend-core/pkg/contracts"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// ---- Persistence Objects ----

// HostNodePO stores persistent configuration and capacity data only.
// Runtime state (status, CPU/mem/disk usage, etc.) is NOT stored here -
// it lives in the NodeStateCache.
type HostNodePO struct {
	ID             string    `gorm:"primaryKey;column:id"`
	Code           string    `gorm:"uniqueIndex;column:code"`
	Location       string    `gorm:"index;column:location"`
	RegionID       string    `gorm:"index;column:region_id"`
	ResourcePoolID *string   `gorm:"index;column:resource_pool_id"`
	Name           string    `gorm:"column:name"`
	Secret         string    `gorm:"column:secret"`
	NodeToken      string    `gorm:"column:node_token;index"`
	CreatedAt      time.Time `gorm:"column:created_at"`

	// Capacity fields
	TotalSlots int  `gorm:"column:total_slots;default:0"`
	UsedSlots  int  `gorm:"column:used_slots;default:0"`
	Enabled    bool `gorm:"column:enabled;default:true"`

	// NAT port pool configuration
	NATPortStart int `gorm:"column:nat_port_start;default:0"`
	NATPortEnd   int `gorm:"column:nat_port_end;default:0"`
}

func (HostNodePO) TableName() string { return "host_nodes" }

type IPAddressPO struct {
	ID         string `gorm:"primaryKey;column:id"`
	NodeID     string `gorm:"index;column:node_id"`
	Address    string `gorm:"column:address"`
	Version    int    `gorm:"column:version"`
	Mode       string `gorm:"column:mode;default:dedicated"` // "dedicated" or "nat"
	Port       int    `gorm:"column:port;default:0"`         // NAT only: high port on host
	InstanceID string `gorm:"column:instance_id"`
}

func (IPAddressPO) TableName() string { return "ip_addresses" }

type TaskPO struct {
	ID         string `gorm:"primaryKey;column:id"`
	NodeID     string `gorm:"index;column:node_id"`
	Type       string `gorm:"column:type"`
	Status     string `gorm:"column:status"`
	SpecJSON   string `gorm:"column:spec;type:text"`
	Error      string `gorm:"column:error"`
	CreatedAt  string `gorm:"column:created_at"`
	FinishedAt string `gorm:"column:finished_at"`
}

func (TaskPO) TableName() string { return "tasks" }

// ---- HostNode Repository ----

type GormHostNodeRepo struct{ db *gorm.DB }

func NewGormHostNodeRepo(db *gorm.DB) *GormHostNodeRepo { return &GormHostNodeRepo{db: db} }

func (r *GormHostNodeRepo) GetByID(id string) (*domain.HostNode, error) {
	var po HostNodePO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("host node not found")
		}
		return nil, err
	}
	return hostToDomain(po), nil
}

func (r *GormHostNodeRepo) GetByCode(code string) (*domain.HostNode, error) {
	var po HostNodePO
	if err := r.db.Where("code = ?", code).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("host node not found")
		}
		return nil, err
	}
	return hostToDomain(po), nil
}

func (r *GormHostNodeRepo) GetByNodeToken(token string) (*domain.HostNode, error) {
	var po HostNodePO
	if err := r.db.Where("node_token = ? AND node_token != ''", token).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("host node not found")
		}
		return nil, err
	}
	return hostToDomain(po), nil
}

func (r *GormHostNodeRepo) ListAll() ([]*domain.HostNode, error) {
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

func (r *GormHostNodeRepo) ListByLocation(loc string) ([]*domain.HostNode, error) {
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

func (r *GormHostNodeRepo) ListByRegionID(regionID string) ([]*domain.HostNode, error) {
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

func (r *GormHostNodeRepo) ListEnabledByRegionID(regionID string) ([]*domain.HostNode, error) {
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

func (r *GormHostNodeRepo) ListByResourcePoolID(poolID string) ([]*domain.HostNode, error) {
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

func (r *GormHostNodeRepo) ListEnabledByResourcePoolID(poolID string) ([]*domain.HostNode, error) {
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

func (r *GormHostNodeRepo) Save(n *domain.HostNode) error {
	po := hostFromDomain(n)
	return r.db.Save(&po).Error
}

// AllocateSlotAtomic atomically increments used_slots using a conditional UPDATE.
// This eliminates the read-modify-write race condition when multiple provisioning
// requests target the same node concurrently.
//
// SQL: UPDATE host_nodes SET used_slots = used_slots + 1
//
//	WHERE id = ? AND enabled = true AND used_slots < total_slots
func (r *GormHostNodeRepo) AllocateSlotAtomic(nodeID string) error {
	result := r.db.Model(&HostNodePO{}).
		Where("id = ? AND enabled = ? AND used_slots < total_slots", nodeID, true).
		Update("used_slots", gorm.Expr("used_slots + 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("domain_error: node has no available slots or is disabled (atomic check)")
	}
	return nil
}

// ReleaseSlotAtomic atomically decrements used_slots using a conditional UPDATE.
func (r *GormHostNodeRepo) ReleaseSlotAtomic(nodeID string) error {
	result := r.db.Model(&HostNodePO{}).
		Where("id = ? AND used_slots > 0", nodeID).
		Update("used_slots", gorm.Expr("used_slots - 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("domain_error: no slots to release (atomic check)")
	}
	return nil
}

// ---- IPAddress Repository ----

type GormIPAddressRepo struct{ db *gorm.DB }

func NewGormIPAddressRepo(db *gorm.DB) *GormIPAddressRepo { return &GormIPAddressRepo{db: db} }

func (r *GormIPAddressRepo) GetByID(id string) (*domain.IPAddress, error) {
	var po IPAddressPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("ip not found")
		}
		return nil, err
	}
	return ipToDomain(po), nil
}

func (r *GormIPAddressRepo) ListByNodeID(nodeID string) ([]*domain.IPAddress, error) {
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

func (r *GormIPAddressRepo) FindByInstanceID(instanceID string) (*domain.IPAddress, error) {
	var po IPAddressPO
	err := r.db.Where("instance_id = ?", instanceID).First(&po).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("ip allocation not found")
		}
		return nil, err
	}
	return ipToDomain(po), nil
}

func (r *GormIPAddressRepo) FindAvailable(nodeID string, version int) (*domain.IPAddress, error) {
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

func (r *GormIPAddressRepo) Save(ip *domain.IPAddress) error {
	po := ipFromDomain(ip)
	return r.db.Save(&po).Error
}

// ListNATPortsByNodeID returns all allocated NAT ports on a node.
func (r *GormIPAddressRepo) ListNATPortsByNodeID(nodeID string) ([]int, error) {
	var ports []int
	err := r.db.Model(&IPAddressPO{}).
		Where("node_id = ? AND mode = ?", nodeID, string(domain.NetworkModeNAT)).
		Pluck("port", &ports).Error
	if err != nil {
		return nil, err
	}
	return ports, nil
}

// FindAvailableNAT returns an available (unassigned) NAT port allocation on the node, if any.
func (r *GormIPAddressRepo) FindAvailableNAT(nodeID string) (*domain.IPAddress, error) {
	var po IPAddressPO
	err := r.db.Where("node_id = ? AND mode = ? AND instance_id = ''", nodeID, string(domain.NetworkModeNAT)).
		First(&po).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("no available NAT port allocation")
		}
		return nil, err
	}
	return ipToDomain(po), nil
}

// ---- Task Repository ----

type GormTaskRepo struct{ db *gorm.DB }

func NewGormTaskRepo(db *gorm.DB) *GormTaskRepo { return &GormTaskRepo{db: db} }

func (r *GormTaskRepo) GetByID(id string) (*contracts.Task, error) {
	var po TaskPO
	if err := r.db.Where("id = ?", id).First(&po).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("task not found")
		}
		return nil, err
	}
	t, err := taskToDomain(po)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *GormTaskRepo) ListPendingByNodeID(nodeID string) ([]contracts.Task, error) {
	var pos []TaskPO
	if err := r.db.Where("node_id = ? AND status = ?", nodeID, string(contracts.TaskStatusQueued)).Find(&pos).Error; err != nil {
		return nil, err
	}
	var err error
	out := make([]contracts.Task, len(pos))
	for i, po := range pos {
		var task contracts.Task
		if task, err = taskToDomain(po); err != nil {
			return nil, err
		}
		out[i] = task
	}
	return out, nil
}

func (r *GormTaskRepo) Save(t *contracts.Task) error {
	po, err := taskFromDomain(*t)
	if err != nil {
		return err
	}
	return r.db.Save(&po).Error
}

// ---- Mapping helpers ----

func hostToDomain(po HostNodePO) *domain.HostNode {
	poolID := ""
	if po.ResourcePoolID != nil {
		poolID = *po.ResourcePoolID
	}
	return domain.ReconstituteHostNodeFull(
		po.ID, po.Code, po.Location, po.RegionID, poolID, po.Name, po.Secret, po.NodeToken,
		po.CreatedAt,
		po.TotalSlots, po.UsedSlots, po.Enabled,
		po.NATPortStart, po.NATPortEnd,
	)
}

func hostFromDomain(n *domain.HostNode) HostNodePO {
	var poolID *string
	if n.ResourcePoolID() != "" {
		s := n.ResourcePoolID()
		poolID = &s
	}
	return HostNodePO{
		ID: n.ID(), Code: n.Code(), Location: n.Location(), RegionID: n.RegionID(),
		ResourcePoolID: poolID, Name: n.Name(), Secret: n.Secret(),
		NodeToken:  n.NodeToken(),
		CreatedAt:  n.CreatedAt(),
		TotalSlots: n.TotalSlots(), UsedSlots: n.UsedSlots(), Enabled: n.Enabled(),
		NATPortStart: n.NATPortStart(), NATPortEnd: n.NATPortEnd(),
	}
}

func ipToDomain(po IPAddressPO) *domain.IPAddress {
	return domain.ReconstituteIPAddressFull(po.ID, po.NodeID, po.Address, po.Version, domain.NetworkMode(po.Mode), po.Port, po.InstanceID)
}

func ipFromDomain(ip *domain.IPAddress) IPAddressPO {
	return IPAddressPO{
		ID: ip.ID(), NodeID: ip.NodeID(), Address: ip.Address(), Version: ip.Version(),
		Mode: string(ip.Mode()), Port: ip.Port(), InstanceID: ip.InstanceID(),
	}
}

func taskToDomain(po TaskPO) (contracts.Task, error) {
	var spec contracts.ProvisionSpec
	err := json.Unmarshal([]byte(po.SpecJSON), &spec)
	if err != nil {
		return contracts.Task{}, err
	}
	return contracts.Task{
		ID: po.ID, NodeID: po.NodeID,
		Type: contracts.TaskType(po.Type), Status: contracts.TaskStatus(po.Status),
		Spec:  spec,
		Error: po.Error, CreatedAt: po.CreatedAt, FinishedAt: po.FinishedAt,
	}, nil
}

func taskFromDomain(t contracts.Task) (TaskPO, error) {
	b, err := json.Marshal(t.Spec)
	if err != nil {
		return TaskPO{}, err
	}
	return TaskPO{
		ID:         t.ID,
		NodeID:     t.NodeID,
		Type:       string(t.Type),
		Status:     string(t.Status),
		SpecJSON:   string(b),
		Error:      t.Error,
		CreatedAt:  t.CreatedAt,
		FinishedAt: t.FinishedAt,
	}, nil
}
