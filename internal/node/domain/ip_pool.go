package domain

import "errors"

// IPAddress represents a single IP in a pool attached to a node.
type IPAddress struct {
	id         string
	nodeID     string
	address    string
	version    int    // 4 or 6
	instanceID string // empty = available
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
	return &IPAddress{id: id, nodeID: nodeID, address: address, version: version}, nil
}

func ReconstituteIPAddress(id, nodeID, address string, version int, instanceID string) *IPAddress {
	return &IPAddress{id: id, nodeID: nodeID, address: address, version: version, instanceID: instanceID}
}

func (ip *IPAddress) ID() string         { return ip.id }
func (ip *IPAddress) NodeID() string     { return ip.nodeID }
func (ip *IPAddress) Address() string    { return ip.address }
func (ip *IPAddress) Version() int       { return ip.version }
func (ip *IPAddress) InstanceID() string { return ip.instanceID }
func (ip *IPAddress) IsAvailable() bool  { return ip.instanceID == "" }

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
