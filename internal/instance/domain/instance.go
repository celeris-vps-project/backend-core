package domain

import (
	"errors"
	"strings"
	"time"
)

const (
	InstanceControlStatusProvisioning = "provisioning"
	InstanceControlStatusActive       = "active"
	InstanceControlStatusSuspended    = "suspended"
	InstanceControlStatusTerminated   = "terminated"

	// Legacy aliases kept for older callers and DB rows. Runtime states
	// "running" and "stopped" are no longer persisted as instance status.
	InstanceStatusPending    = InstanceControlStatusProvisioning
	InstanceStatusSuspended  = InstanceControlStatusSuspended
	InstanceStatusTerminated = InstanceControlStatusTerminated
	InstanceStatusRunning    = "running"
	InstanceStatusStopped    = "stopped"

	InstanceSuspendReasonTrafficRunOut = "traffic_run_out"
)

type Instance struct {
	id              string
	customerID      string
	orderID         string
	nodeID          string
	hostname        string
	plan            string
	os              string
	cpu             int
	memoryMB        int
	diskGB          int
	bandwidthGB     int
	ipv4            string
	ipv6            string
	hostIP          string
	controlStatus   string
	suspendReason   string
	initialPassword string

	// NAT mode fields
	networkMode string // "dedicated" or "nat"; empty defaults to "dedicated"
	natPort     int    // NAT mode: the high port on the host mapped to VM SSH

	createdAt    time.Time
	startedAt    *time.Time
	stoppedAt    *time.Time
	suspendedAt  *time.Time
	terminatedAt *time.Time
}

func NewInstance(id, customerID, orderID, nodeID, hostname, plan, os, networkMode string, cpu, memoryMB, diskGB, bandwidthGB int) (*Instance, error) {
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
	if bandwidthGB < 0 {
		return nil, errors.New("domain_error: bandwidth must be >= 0")
	}
	return &Instance{
		id: id, customerID: customerID, orderID: orderID, nodeID: nodeID,
		hostname: hostname, plan: plan, os: os,
		cpu: cpu, memoryMB: memoryMB, diskGB: diskGB, bandwidthGB: bandwidthGB,
		controlStatus: InstanceControlStatusProvisioning, createdAt: time.Now(),
		networkMode: networkMode,
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
		ipv4: ipv4, ipv6: ipv6, controlStatus: normalizeControlStatus(status),
		createdAt: createdAt, startedAt: startedAt, stoppedAt: stoppedAt,
		suspendedAt: suspendedAt, terminatedAt: terminatedAt,
	}
}

// ReconstituteInstanceFull reconstructs an Instance with all fields including NAT support.
func ReconstituteInstanceFull(
	id, customerID, orderID, nodeID, hostname, plan, os string,
	cpu, memoryMB, diskGB, bandwidthGB int,
	ipv4, ipv6, hostIP, status, suspendReason, initialPassword string,
	networkMode string, natPort int,
	createdAt time.Time,
	startedAt, stoppedAt, suspendedAt, terminatedAt *time.Time,
) *Instance {
	return &Instance{
		id: id, customerID: customerID, orderID: orderID, nodeID: nodeID,
		hostname: hostname, plan: plan, os: os,
		cpu: cpu, memoryMB: memoryMB, diskGB: diskGB, bandwidthGB: bandwidthGB,
		ipv4: ipv4, ipv6: ipv6, hostIP: hostIP, controlStatus: normalizeControlStatus(status),
		suspendReason:   strings.TrimSpace(suspendReason),
		initialPassword: initialPassword,
		networkMode:     networkMode, natPort: natPort,
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
func (i *Instance) BandwidthGB() int         { return i.bandwidthGB }
func (i *Instance) IPv4() string             { return i.ipv4 }
func (i *Instance) IPv6() string             { return i.ipv6 }
func (i *Instance) HostIP() string           { return i.hostIP }
func (i *Instance) Status() string           { return i.ControlStatus() }
func (i *Instance) ControlStatus() string    { return normalizeControlStatus(i.controlStatus) }
func (i *Instance) SuspendReason() string    { return i.suspendReason }
func (i *Instance) InitialPassword() string  { return i.initialPassword }
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

func (i *Instance) NATPort() int                       { return i.natPort }
func (i *Instance) IsNAT() bool                        { return i.networkMode == "nat" }
func (i *Instance) SetNetworkMode(mode string)         { i.networkMode = mode }
func (i *Instance) SetNATPort(port int)                { i.natPort = port }
func (i *Instance) SetInitialPassword(password string) { i.initialPassword = password }
func (i *Instance) SetHostIP(hostIP string)            { i.hostIP = hostIP }
func (i *Instance) SetSuspendReason(reason string)     { i.suspendReason = strings.TrimSpace(reason) }

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

func (i *Instance) MarkProvisioned(at time.Time) error {
	if i.ControlStatus() == InstanceControlStatusTerminated {
		return errors.New("domain_error: terminated instances cannot be provisioned")
	}
	i.controlStatus = InstanceControlStatusActive
	i.startedAt = &at
	i.stoppedAt = nil
	return nil
}

func (i *Instance) Start(at time.Time) error {
	switch i.ControlStatus() {
	case InstanceControlStatusTerminated:
		return errors.New("domain_error: terminated instances cannot be started")
	case InstanceControlStatusSuspended:
		return errors.New("domain_error: suspended instances must be unsuspended first")
	case InstanceControlStatusProvisioning:
		return errors.New("domain_error: provisioning instances cannot be started")
	}
	i.startedAt = &at
	i.stoppedAt = nil
	return nil
}

func (i *Instance) Stop(at time.Time) error {
	switch i.ControlStatus() {
	case InstanceControlStatusTerminated:
		return errors.New("domain_error: terminated instances cannot be stopped")
	case InstanceControlStatusSuspended:
		return errors.New("domain_error: suspended instances cannot be stopped")
	case InstanceControlStatusProvisioning:
		return errors.New("domain_error: provisioning instances cannot be stopped")
	}
	i.stoppedAt = &at
	return nil
}

func (i *Instance) Suspend(at time.Time) error {
	if i.ControlStatus() == InstanceControlStatusTerminated {
		return errors.New("domain_error: terminated instances cannot be suspended")
	}
	if i.ControlStatus() == InstanceControlStatusSuspended {
		return errors.New("domain_error: instance already suspended")
	}
	if i.ControlStatus() == InstanceControlStatusProvisioning {
		return errors.New("domain_error: provisioning instances cannot be suspended")
	}
	i.controlStatus = InstanceControlStatusSuspended
	i.suspendedAt = &at
	return nil
}

func (i *Instance) SuspendWithReason(at time.Time, reason string) error {
	if err := i.Suspend(at); err != nil {
		return err
	}
	i.SetSuspendReason(reason)
	return nil
}

func (i *Instance) Unsuspend(at time.Time) error {
	if i.ControlStatus() != InstanceControlStatusSuspended {
		return errors.New("domain_error: only suspended instances can be unsuspended")
	}
	i.controlStatus = InstanceControlStatusActive
	i.startedAt = &at
	i.suspendedAt = nil
	i.suspendReason = ""
	return nil
}

// RecoverFromBillingSuspension clears the control suspension after overdue
// payment is received. The runtime VM remains stopped until the user starts it.
func (i *Instance) RecoverFromBillingSuspension(at time.Time) error {
	if i.ControlStatus() != InstanceControlStatusSuspended {
		return errors.New("domain_error: only suspended instances can recover")
	}
	i.controlStatus = InstanceControlStatusActive
	i.stoppedAt = &at
	i.suspendedAt = nil
	i.suspendReason = ""
	return nil
}

func (i *Instance) Terminate(at time.Time) error {
	if i.ControlStatus() == InstanceControlStatusTerminated {
		return errors.New("domain_error: instance already terminated")
	}
	i.controlStatus = InstanceControlStatusTerminated
	i.terminatedAt = &at
	return nil
}

func normalizeControlStatus(status string) string {
	switch status {
	case InstanceControlStatusProvisioning, "pending", "":
		return InstanceControlStatusProvisioning
	case InstanceControlStatusActive, InstanceStatusRunning, InstanceStatusStopped:
		return InstanceControlStatusActive
	case InstanceControlStatusSuspended:
		return InstanceControlStatusSuspended
	case InstanceControlStatusTerminated:
		return InstanceControlStatusTerminated
	default:
		return status
	}
}
