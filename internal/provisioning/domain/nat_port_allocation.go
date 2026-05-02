package domain

import (
	"errors"
	"strings"
)

const (
	NATProtocolTCP = "tcp"
	DefaultSSHPort = 22
)

type NATPortAllocation struct {
	id           string
	allocationID string
	nodeID       string
	instanceID   string
	guestIP      string
	protocol     string
	hostPort     int
	guestPort    int
}

func NewNATPortForwardAllocation(id, allocationID, nodeID, instanceID, guestIP, protocol string, hostPort, guestPort int) (*NATPortAllocation, error) {
	if id == "" {
		return nil, errors.New("domain_error: NAT allocation id is required")
	}
	if allocationID == "" {
		return nil, errors.New("domain_error: NAT allocation group id is required")
	}
	if nodeID == "" {
		return nil, errors.New("domain_error: node id is required")
	}
	if instanceID == "" {
		return nil, errors.New("domain_error: instance id is required")
	}
	if guestIP == "" {
		return nil, errors.New("domain_error: guest ip is required")
	}
	protocol = normalizeNATProtocol(protocol)
	if hostPort <= 0 || hostPort > 65535 {
		return nil, errors.New("domain_error: host port must be between 1 and 65535")
	}
	if guestPort <= 0 || guestPort > 65535 {
		return nil, errors.New("domain_error: guest port must be between 1 and 65535")
	}
	return &NATPortAllocation{
		id:           id,
		allocationID: allocationID,
		nodeID:       nodeID,
		instanceID:   instanceID,
		guestIP:      guestIP,
		protocol:     protocol,
		hostPort:     hostPort,
		guestPort:    guestPort,
	}, nil
}

func ReconstituteNATPortAllocation(id, allocationID, nodeID, instanceID, guestIP, protocol string, hostPort, guestPort int) *NATPortAllocation {
	return &NATPortAllocation{
		id:           id,
		allocationID: allocationID,
		nodeID:       nodeID,
		instanceID:   instanceID,
		guestIP:      guestIP,
		protocol:     normalizeNATProtocol(protocol),
		hostPort:     hostPort,
		guestPort:    guestPort,
	}
}

func (a *NATPortAllocation) ID() string           { return a.id }
func (a *NATPortAllocation) AllocationID() string { return a.allocationID }
func (a *NATPortAllocation) NodeID() string       { return a.nodeID }
func (a *NATPortAllocation) InstanceID() string   { return a.instanceID }
func (a *NATPortAllocation) GuestIP() string      { return a.guestIP }
func (a *NATPortAllocation) Protocol() string     { return a.protocol }
func (a *NATPortAllocation) HostPort() int        { return a.hostPort }
func (a *NATPortAllocation) GuestPort() int       { return a.guestPort }

func normalizeNATProtocol(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return NATProtocolTCP
	}
	return protocol
}
