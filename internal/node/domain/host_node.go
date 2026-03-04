package domain

import (
	"errors"
	"time"
)

const (
	HostStatusOnline  = "online"
	HostStatusOffline = "offline"
)

// HostNode represents a physical server that runs the agent.
// It also tracks capacity (slots) for instance allocation — previously
// split across a separate "Node" type in the instance bounded context.
type HostNode struct {
	id         string
	code       string // e.g. "DE-fra-01"
	location   string // e.g. "DE-fra"
	regionID   string // FK to Region
	name       string
	secret     string // shared secret for agent auth
	ip         string
	status     string
	agentVer   string
	cpuUsage   float64
	memUsage   float64
	diskUsage  float64
	vmCount    int
	lastSeenAt *time.Time
	createdAt  time.Time

	// Capacity management (merged from instance/domain.Node)
	totalSlots int  // max instances this node can host
	usedSlots  int  // currently allocated instances
	enabled    bool // whether the node accepts new purchases
}

func NewHostNode(id, code, location, name, secret string) (*HostNode, error) {
	if id == "" {
		return nil, errors.New("domain_error: id is required")
	}
	if code == "" {
		return nil, errors.New("domain_error: code is required")
	}
	if location == "" {
		return nil, errors.New("domain_error: location is required")
	}
	if name == "" {
		return nil, errors.New("domain_error: name is required")
	}
	if secret == "" {
		return nil, errors.New("domain_error: secret is required")
	}
	return &HostNode{
		id: id, code: code, location: location, name: name, secret: secret,
		status: HostStatusOffline, createdAt: time.Now(),
		enabled: true,
	}, nil
}

func ReconstituteHostNode(
	id, code, location, regionID, name, secret, ip, status, agentVer string,
	cpuUsage, memUsage, diskUsage float64, vmCount int,
	lastSeenAt *time.Time, createdAt time.Time,
	totalSlots, usedSlots int, enabled bool,
) *HostNode {
	return &HostNode{
		id: id, code: code, location: location, regionID: regionID, name: name, secret: secret,
		ip: ip, status: status, agentVer: agentVer,
		cpuUsage: cpuUsage, memUsage: memUsage, diskUsage: diskUsage,
		vmCount: vmCount, lastSeenAt: lastSeenAt, createdAt: createdAt,
		totalSlots: totalSlots, usedSlots: usedSlots, enabled: enabled,
	}
}

func (n *HostNode) ID() string             { return n.id }
func (n *HostNode) Code() string           { return n.code }
func (n *HostNode) Location() string       { return n.location }
func (n *HostNode) RegionID() string       { return n.regionID }
func (n *HostNode) SetRegionID(id string)  { n.regionID = id }
func (n *HostNode) Name() string           { return n.name }
func (n *HostNode) Secret() string         { return n.secret }
func (n *HostNode) IP() string             { return n.ip }
func (n *HostNode) Status() string         { return n.status }
func (n *HostNode) AgentVer() string       { return n.agentVer }
func (n *HostNode) CPUUsage() float64      { return n.cpuUsage }
func (n *HostNode) MemUsage() float64      { return n.memUsage }
func (n *HostNode) DiskUsage() float64     { return n.diskUsage }
func (n *HostNode) VMCount() int           { return n.vmCount }
func (n *HostNode) LastSeenAt() *time.Time { return n.lastSeenAt }
func (n *HostNode) CreatedAt() time.Time   { return n.createdAt }

// ---- Capacity accessors ----

func (n *HostNode) TotalSlots() int { return n.totalSlots }
func (n *HostNode) UsedSlots() int  { return n.usedSlots }
func (n *HostNode) Enabled() bool   { return n.enabled }

func (n *HostNode) SetTotalSlots(slots int) { n.totalSlots = slots }

func (n *HostNode) AvailableSlots() int {
	avail := n.totalSlots - n.usedSlots
	if avail < 0 {
		return 0
	}
	return avail
}

func (n *HostNode) HasCapacity() bool {
	return n.enabled && n.AvailableSlots() > 0
}

// AllocateSlot reserves one slot on this node. Returns error if no capacity.
func (n *HostNode) AllocateSlot() error {
	if !n.enabled {
		return errors.New("domain_error: node is disabled")
	}
	if n.AvailableSlots() <= 0 {
		return errors.New("domain_error: node has no available slots")
	}
	n.usedSlots++
	return nil
}

// ReleaseSlot frees one slot on this node.
func (n *HostNode) ReleaseSlot() error {
	if n.usedSlots <= 0 {
		return errors.New("domain_error: no slots to release")
	}
	n.usedSlots--
	return nil
}

func (n *HostNode) Enable()  { n.enabled = true }
func (n *HostNode) Disable() { n.enabled = false }

// ---- Agent lifecycle ----

func (n *HostNode) Register(ip, agentVer string, at time.Time) {
	n.ip = ip
	n.agentVer = agentVer
	n.status = HostStatusOnline
	n.lastSeenAt = &at
}

func (n *HostNode) RecordHeartbeat(cpuUsage, memUsage, diskUsage float64, vmCount int, at time.Time) {
	n.cpuUsage = cpuUsage
	n.memUsage = memUsage
	n.diskUsage = diskUsage
	n.vmCount = vmCount
	n.status = HostStatusOnline
	n.lastSeenAt = &at
}

func (n *HostNode) MarkOffline() {
	n.status = HostStatusOffline
}

func (n *HostNode) ValidateSecret(s string) bool {
	return n.secret == s
}
