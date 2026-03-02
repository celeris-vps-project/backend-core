package domain

import "errors"

// Node represents a physical/logical server location that hosts VPS instances.
// Nodes are grouped by location code, e.g. "DE-fra", "US-slc".
type Node struct {
	id         string
	code       string // e.g. "DE-fra-01"
	location   string // e.g. "DE-fra"
	name       string // display name, e.g. "Frankfurt #1"
	totalSlots int    // max instances this node can host
	usedSlots  int    // currently allocated instances
	enabled    bool   // whether the node accepts new purchases
}

func NewNode(id, code, location, name string, totalSlots int) (*Node, error) {
	if id == "" {
		return nil, errors.New("domain_error: node id is required")
	}
	if code == "" {
		return nil, errors.New("domain_error: node code is required")
	}
	if location == "" {
		return nil, errors.New("domain_error: location is required")
	}
	if name == "" {
		return nil, errors.New("domain_error: name is required")
	}
	if totalSlots <= 0 {
		return nil, errors.New("domain_error: total slots must be > 0")
	}
	return &Node{
		id:         id,
		code:       code,
		location:   location,
		name:       name,
		totalSlots: totalSlots,
		usedSlots:  0,
		enabled:    true,
	}, nil
}

func ReconstituteNode(id, code, location, name string, totalSlots, usedSlots int, enabled bool) *Node {
	return &Node{
		id:         id,
		code:       code,
		location:   location,
		name:       name,
		totalSlots: totalSlots,
		usedSlots:  usedSlots,
		enabled:    enabled,
	}
}

func (n *Node) ID() string       { return n.id }
func (n *Node) Code() string     { return n.code }
func (n *Node) Location() string { return n.location }
func (n *Node) Name() string     { return n.name }
func (n *Node) TotalSlots() int  { return n.totalSlots }
func (n *Node) UsedSlots() int   { return n.usedSlots }
func (n *Node) Enabled() bool    { return n.enabled }

func (n *Node) AvailableSlots() int {
	avail := n.totalSlots - n.usedSlots
	if avail < 0 {
		return 0
	}
	return avail
}

func (n *Node) HasCapacity() bool {
	return n.enabled && n.AvailableSlots() > 0
}

// AllocateSlot reserves one slot on this node. Returns error if no capacity.
func (n *Node) AllocateSlot() error {
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
func (n *Node) ReleaseSlot() error {
	if n.usedSlots <= 0 {
		return errors.New("domain_error: no slots to release")
	}
	n.usedSlots--
	return nil
}

func (n *Node) Enable() {
	n.enabled = true
}

func (n *Node) Disable() {
	n.enabled = false
}
