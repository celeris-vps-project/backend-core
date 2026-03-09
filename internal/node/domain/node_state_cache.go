package domain

import "time"

// NodeState holds runtime state reported by an agent (heartbeat / register).
// This data is ephemeral and lives in the cache layer, NOT in the database.
type NodeState struct {
	Status     string    // "online" / "offline"
	IP         string    // agent's public IP
	AgentVer   string    // agent software version
	CPUUsage   float64   // 0-100
	MemUsage   float64   // 0-100
	DiskUsage  float64   // 0-100
	VMCount    int       // number of running VMs
	LastSeenAt time.Time // last heartbeat timestamp
}

// NodeStateCache abstracts the storage of node runtime state.
// Implementations may use in-memory maps, Redis, etc.
//
// The cache layer is responsible for TTL-based expiration: if an agent
// stops sending heartbeats, GetNodeState should return nil so the node
// is considered offline.
type NodeStateCache interface {
	// SetNodeState stores or updates the runtime state for a node.
	SetNodeState(nodeID string, state *NodeState) error

	// GetNodeState returns the cached state, or nil if the entry has
	// expired or does not exist (i.e. the node is offline).
	GetNodeState(nodeID string) (*NodeState, error)

	// GetAllNodeStates returns all currently cached (non-expired) states.
	GetAllNodeStates() (map[string]*NodeState, error)

	// DeleteNodeState removes a node's cached state explicitly.
	DeleteNodeState(nodeID string) error
}
