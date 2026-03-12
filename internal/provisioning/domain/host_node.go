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
// It stores persistent configuration and capacity data only.
// Runtime state (status, CPU/mem/disk usage, etc.) is held in
// NodeStateCache and NOT persisted to the database.
type HostNode struct {
	id             string
	code           string // e.g. "DE-fra-01"
	location       string // e.g. "DE-fra"
	regionID       string // FK to Region
	resourcePoolID string // FK to ResourcePool
	name           string
	secret         string // shared secret for agent auth (legacy / bootstrap fallback)
	nodeToken      string // permanent opaque token issued during bootstrap registration
	createdAt      time.Time

	// Capacity management
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
	if secret == "" {
		return nil, errors.New("domain_error: secret is required")
	}
	return &HostNode{
		id: id, code: code, location: location, name: name, secret: secret,
		createdAt: time.Now(),
		enabled:   true,
	}, nil
}

func ReconstituteHostNode(
	id, code, location, regionID, resourcePoolID, name, secret, nodeToken string,
	createdAt time.Time,
	totalSlots, usedSlots int, enabled bool,
) *HostNode {
	return &HostNode{
		id: id, code: code, location: location, regionID: regionID, resourcePoolID: resourcePoolID,
		name: name, secret: secret, nodeToken: nodeToken,
		createdAt:  createdAt,
		totalSlots: totalSlots, usedSlots: usedSlots, enabled: enabled,
	}
}

func (n *HostNode) ID() string                  { return n.id }
func (n *HostNode) Code() string                { return n.code }
func (n *HostNode) Location() string            { return n.location }
func (n *HostNode) RegionID() string            { return n.regionID }
func (n *HostNode) SetRegionID(id string)       { n.regionID = id }
func (n *HostNode) ResourcePoolID() string      { return n.resourcePoolID }
func (n *HostNode) SetResourcePoolID(id string) { n.resourcePoolID = id }
func (n *HostNode) Name() string                { return n.name }
func (n *HostNode) Secret() string              { return n.secret }
func (n *HostNode) NodeToken() string           { return n.nodeToken }
func (n *HostNode) SetNodeToken(t string)       { n.nodeToken = t }
func (n *HostNode) RevokeNodeToken()            { n.nodeToken = "" }
func (n *HostNode) CreatedAt() time.Time        { return n.createdAt }

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

func (n *HostNode) ValidateSecret(s string) bool {
	return n.secret == s
}

// ValidateNodeToken checks the permanent node credential issued during bootstrap.
func (n *HostNode) ValidateNodeToken(t string) bool {
	return n.nodeToken != "" && n.nodeToken == t
}
