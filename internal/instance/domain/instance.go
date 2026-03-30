package domain

import (
	"errors"
	"time"
)

const (
	InstanceStatusPending    = "pending"
	InstanceStatusRunning    = "running"
	InstanceStatusStopped    = "stopped"
	InstanceStatusSuspended  = "suspended"
	InstanceStatusTerminated = "terminated"
)

type Instance struct {
	id         string
	customerID string
	orderID    string
	nodeID     string
	hostname   string
	plan       string
	os         string
	cpu        int
	memoryMB   int
	diskGB     int
	ipv4       string
	ipv6       string
	status     string

	// NAT mode fields
	networkMode string // "dedicated" or "nat"; empty defaults to "dedicated"
	natPort     int    // NAT mode: the high port on the host mapped to VM SSH

	createdAt    time.Time
	startedAt    *time.Time
	stoppedAt    *time.Time
	suspendedAt  *time.Time
	terminatedAt *time.Time
}

func NewInstance(id, customerID, orderID, nodeID, hostname, plan, os string, cpu, memoryMB, diskGB int) (*Instance, error) {
	if id == "" {
		return nil, errors.New("domain_error: instance id is required")
	}
	if customerID == "" {
		return nil, errors.New("domain_error: customer id is required")
	}
	if orderID == "" {
		return nil, errors.New("domain_error: order id is required")
	}
	if hostname == "" {
		return nil, errors.New("domain_error: hostname is required")
	}
	if plan == "" {
		return nil, errors.New("domain_error: plan is required")
	}
	if os == "" {
		return nil, errors.New("domain_error: os is required")
	}
	if cpu <= 0 {
		return nil, errors.New("domain_error: cpu must be > 0")
	}
	if memoryMB <= 0 {
		return nil, errors.New("domain_error: memory must be > 0")
	}
	if diskGB <= 0 {
		return nil, errors.New("domain_error: disk must be > 0")
	}
	return &Instance{
		id: id, customerID: customerID, orderID: orderID, nodeID: nodeID,
		hostname: hostname, plan: plan, os: os,
		cpu: cpu, memoryMB: memoryMB, diskGB: diskGB,
		status: InstanceStatusPending, createdAt: time.Now(),
	}, nil
}

func ReconstituteInstance(
	id, customerID, orderID, nodeID, hostname, plan, os string,
	cpu, memoryMB, diskGB int,
	ipv4, ipv6, status string,
	createdAt time.Time,
	startedAt, stoppedAt, suspendedAt, terminatedAt *time.Time,
) *Instance {
	return &Instance{
		id: id, customerID: customerID, orderID: orderID, nodeID: nodeID,
		hostname: hostname, plan: plan, os: os,
		cpu: cpu, memoryMB: memoryMB, diskGB: diskGB,
		ipv4: ipv4, ipv6: ipv6, status: status,
		createdAt: createdAt, startedAt: startedAt, stoppedAt: stoppedAt,
		suspendedAt: suspendedAt, terminatedAt: terminatedAt,
	}
}

// ReconstituteInstanceFull reconstructs an Instance with all fields including NAT support.
func ReconstituteInstanceFull(
	id, customerID, orderID, nodeID, hostname, plan, os string,
	cpu, memoryMB, diskGB int,
	ipv4, ipv6, status string,
	networkMode string, natPort int,
	createdAt time.Time,
	startedAt, stoppedAt, suspendedAt, terminatedAt *time.Time,
) *Instance {
	return &Instance{
		id: id, customerID: customerID, orderID: orderID, nodeID: nodeID,
		hostname: hostname, plan: plan, os: os,
		cpu: cpu, memoryMB: memoryMB, diskGB: diskGB,
		ipv4: ipv4, ipv6: ipv6, status: status,
		networkMode: networkMode, natPort: natPort,
		createdAt: createdAt, startedAt: startedAt, stoppedAt: stoppedAt,
		suspendedAt: suspendedAt, terminatedAt: terminatedAt,
	}
}

func (i *Instance) ID() string               { return i.id }
func (i *Instance) CustomerID() string       { return i.customerID }
func (i *Instance) OrderID() string          { return i.orderID }
func (i *Instance) NodeID() string           { return i.nodeID }
func (i *Instance) Hostname() string         { return i.hostname }
func (i *Instance) Plan() string             { return i.plan }
func (i *Instance) OS() string               { return i.os }
func (i *Instance) CPU() int                 { return i.cpu }
func (i *Instance) MemoryMB() int            { return i.memoryMB }
func (i *Instance) DiskGB() int              { return i.diskGB }
func (i *Instance) IPv4() string             { return i.ipv4 }
func (i *Instance) IPv6() string             { return i.ipv6 }
func (i *Instance) Status() string           { return i.status }
func (i *Instance) CreatedAt() time.Time     { return i.createdAt }
func (i *Instance) StartedAt() *time.Time    { return i.startedAt }
func (i *Instance) StoppedAt() *time.Time    { return i.stoppedAt }
func (i *Instance) SuspendedAt() *time.Time  { return i.suspendedAt }
func (i *Instance) TerminatedAt() *time.Time { return i.terminatedAt }

// ---- NAT accessors ----

// NetworkMode returns the network mode: "dedicated" or "nat".
// Empty string is treated as "dedicated" for backward compatibility.
func (i *Instance) NetworkMode() string {
	if i.networkMode == "" {
		return "dedicated"
	}
	return i.networkMode
}

func (i *Instance) NATPort() int               { return i.natPort }
func (i *Instance) IsNAT() bool                { return i.networkMode == "nat" }
func (i *Instance) SetNetworkMode(mode string) { i.networkMode = mode }
func (i *Instance) SetNATPort(port int)        { i.natPort = port }

// AssignNode records the host node that fulfilled this instance.
func (i *Instance) AssignNode(nodeID string) error {
	if nodeID == "" {
		return errors.New("domain_error: node id is required")
	}
	i.nodeID = nodeID
	return nil
}

// AssignNAT sets the NAT network mode and port for this instance.
func (i *Instance) AssignNAT(port int) error {
	if port <= 0 || port > 65535 {
		return errors.New("domain_error: NAT port must be between 1 and 65535")
	}
	i.networkMode = "nat"
	i.natPort = port
	return nil
}

func (i *Instance) AssignIP(ipv4, ipv6 string) error {
	if ipv4 == "" && ipv6 == "" {
		return errors.New("domain_error: at least one IP is required")
	}
	i.ipv4 = ipv4
	i.ipv6 = ipv6
	return nil
}

func (i *Instance) Start(at time.Time) error {
	if i.status != InstanceStatusPending && i.status != InstanceStatusStopped {
		return errors.New("domain_error: can only start from pending or stopped")
	}
	i.status = InstanceStatusRunning
	i.startedAt = &at
	i.stoppedAt = nil
	return nil
}

func (i *Instance) Stop(at time.Time) error {
	if i.status != InstanceStatusRunning {
		return errors.New("domain_error: only running instances can be stopped")
	}
	i.status = InstanceStatusStopped
	i.stoppedAt = &at
	return nil
}

func (i *Instance) Suspend(at time.Time) error {
	if i.status == InstanceStatusTerminated {
		return errors.New("domain_error: terminated instances cannot be suspended")
	}
	if i.status == InstanceStatusSuspended {
		return errors.New("domain_error: instance already suspended")
	}
	i.status = InstanceStatusSuspended
	i.suspendedAt = &at
	return nil
}

func (i *Instance) Unsuspend(at time.Time) error {
	if i.status != InstanceStatusSuspended {
		return errors.New("domain_error: only suspended instances can be unsuspended")
	}
	i.status = InstanceStatusRunning
	i.startedAt = &at
	i.suspendedAt = nil
	return nil
}

// RecoverFromBillingSuspension returns a suspended instance to stopped state
// after overdue payment is received. The user must start it manually.
func (i *Instance) RecoverFromBillingSuspension(at time.Time) error {
	if i.status != InstanceStatusSuspended {
		return errors.New("domain_error: only suspended instances can recover to stopped")
	}
	i.status = InstanceStatusStopped
	i.stoppedAt = &at
	i.suspendedAt = nil
	return nil
}

func (i *Instance) Terminate(at time.Time) error {
	if i.status == InstanceStatusTerminated {
		return errors.New("domain_error: instance already terminated")
	}
	i.status = InstanceStatusTerminated
	i.terminatedAt = &at
	return nil
}
