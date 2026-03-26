package domain

import "errors"

// NetworkMode determines how an IP resource is used.
type NetworkMode string

const (
	// NetworkModeDedicated means one public IP per instance (traditional).
	NetworkModeDedicated NetworkMode = "dedicated"
	// NetworkModeNAT means the instance shares the host's public IP via port mapping.
	NetworkModeNAT NetworkMode = "nat"
)

// IPAddress represents a single network resource in a pool attached to a node.
//
// In dedicated mode, each entry is a unique public IP address.
// In NAT mode, each entry is a port allocation on the host's shared IP.
// The host IP itself is NOT stored here — it is resolved at runtime from
// NodeStateCache (agent registration). This avoids stale data if the host
// IP changes.
type IPAddress struct {
	id         string
	nodeID     string
	address    string      // dedicated: the public IP; NAT: empty (resolved at runtime)
	version    int         // 4 or 6
	mode       NetworkMode // "dedicated" or "nat"; defaults to "dedicated" for backward compat
	port       int         // NAT only: the allocated high port on the host (e.g. 20001)
	instanceID string      // empty = available
}

func NewIPAddress(id, nodeID, address string, version int) (*IPAddress, error) {
	if id == "" {
		return nil, errors.New("domain_error: ip id is required")
	}
	if nodeID == "" {
		return nil, errors.New("domain_error: node id is required")
	}
	if address == "" {
		return nil, errors.New("domain_error: address is required")
	}
	if version != 4 && version != 6 {
		return nil, errors.New("domain_error: version must be 4 or 6")
	}
	return &IPAddress{id: id, nodeID: nodeID, address: address, version: version, mode: NetworkModeDedicated}, nil
}

// NewNATPortAllocation creates an IPAddress entry for NAT mode.
// The address field is left empty — the host IP is resolved at runtime from NodeStateCache.
func NewNATPortAllocation(id, nodeID string, port int) (*IPAddress, error) {
	if id == "" {
		return nil, errors.New("domain_error: ip id is required")
	}
	if nodeID == "" {
		return nil, errors.New("domain_error: node id is required")
	}
	if port <= 0 || port > 65535 {
		return nil, errors.New("domain_error: port must be between 1 and 65535")
	}
	return &IPAddress{
		id:      id,
		nodeID:  nodeID,
		address: "", // resolved at runtime from NodeStateCache
		version: 4,
		mode:    NetworkModeNAT,
		port:    port,
	}, nil
}

func ReconstituteIPAddress(id, nodeID, address string, version int, instanceID string) *IPAddress {
	return &IPAddress{id: id, nodeID: nodeID, address: address, version: version, mode: NetworkModeDedicated, instanceID: instanceID}
}

// ReconstituteIPAddressFull reconstructs an IPAddress with all fields including NAT support.
func ReconstituteIPAddressFull(id, nodeID, address string, version int, mode NetworkMode, port int, instanceID string) *IPAddress {
	if mode == "" {
		mode = NetworkModeDedicated
	}
	return &IPAddress{
		id: id, nodeID: nodeID, address: address, version: version,
		mode: mode, port: port, instanceID: instanceID,
	}
}

func (ip *IPAddress) ID() string         { return ip.id }
func (ip *IPAddress) NodeID() string     { return ip.nodeID }
func (ip *IPAddress) Address() string    { return ip.address }
func (ip *IPAddress) Version() int       { return ip.version }
func (ip *IPAddress) Mode() NetworkMode  { return ip.mode }
func (ip *IPAddress) Port() int          { return ip.port }
func (ip *IPAddress) InstanceID() string { return ip.instanceID }
func (ip *IPAddress) IsAvailable() bool  { return ip.instanceID == "" }
func (ip *IPAddress) IsNAT() bool        { return ip.mode == NetworkModeNAT }
func (ip *IPAddress) IsDedicated() bool  { return ip.mode == NetworkModeDedicated }

func (ip *IPAddress) Assign(instanceID string) error {
	if !ip.IsAvailable() {
		return errors.New("domain_error: ip already assigned")
	}
	if instanceID == "" {
		return errors.New("domain_error: instance id is required")
	}
	ip.instanceID = instanceID
	return nil
}

func (ip *IPAddress) Release() { ip.instanceID = "" }
