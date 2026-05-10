package vm

import (
	"backend-core/pkg/contracts"
	"fmt"
	"io"
	"time"
)

// NetworkStats is used to calculate network in period "Like in a month". Would fetch from interface Usually reset on server reboot
type NetworkStats struct {
	Total uint64 `json:"period_stats_bytes"` // current total
	RX    uint64 `json:"rx_bytes"`
	TX    uint64 `json:"tx_bytes"`
}

// VMInfo is the runtime state returned after querying a guest.
type VMInfo struct {
	InstanceID   string       `json:"instance_id"`
	State        string       `json:"state"` // "running", "stopped", "paused", etc.
	CPU          int          `json:"cpu"`
	MemoryMB     int          `json:"memory_mb"`
	DiskGB       int          `json:"disk_gb"`
	IPv4         string       `json:"ipv4,omitempty"`
	IPv6         string       `json:"ipv6,omitempty"`
	NetworkStats NetworkStats `json:"network_stats"`
}

// Hypervisor is the abstracted interface for managing virtual machines or
// containers on a host node. Two production backends are supported:
//
//   - LibvirtDriver  — manages KVM guests and LXC containers via libvirt.
//   - IncusDriver    — manages instances via the Incus (LXD-fork) API/CLI.
//
// A StubDriver is provided for development and testing.
type Hypervisor interface {
	// Create provisions a new VM/container from the given spec.
	Create(spec contracts.ProvisionSpec) error

	// Start boots an existing stopped guest.
	Start(instanceID string) error

	// Stop gracefully shuts down a running guest.
	Stop(instanceID string) error

	// Reboot cycles a running guest.
	Reboot(instanceID string) error

	// Destroy permanently removes a guest and its storage.
	Destroy(instanceID string) error

	// Info returns the current runtime state of a guest.
	Info(instanceID string) (*VMInfo, error)

	// List returns the runtime state of all guests on the host.
	List() ([]*VMInfo, error)
}

// BootWaiter is an optional interface that a Hypervisor can implement to
// provide polling-based boot confirmation. After a VM is created or started,
// the caller can use WaitForBoot to poll the hypervisor until the guest reaches
// the driver's ready state. Guest-agent IP discovery is optional.
type BootWaiter interface {
	// WaitForBoot polls the hypervisor until the VM is ready or the timeout
	// expires. Returns the final VMInfo on success.
	WaitForBoot(instanceID string, timeout time.Duration) (*VMInfo, error)
}

type ConsoleConnector interface {
	OpenConsole(instanceID string) (io.ReadWriteCloser, error)
}

// Execute dispatches a task to the appropriate Hypervisor method.
func Execute(h Hypervisor, task contracts.Task) error {
	switch task.Type {
	case contracts.TaskProvision:
		return h.Create(task.Spec)
	case contracts.TaskStart:
		return h.Start(task.Spec.InstanceID)
	case contracts.TaskStop:
		return h.Stop(task.Spec.InstanceID)
	case contracts.TaskReboot:
		return h.Reboot(task.Spec.InstanceID)
	case contracts.TaskDeprovision:
		return h.Destroy(task.Spec.InstanceID)
	case contracts.TaskSuspend:
		return h.Stop(task.Spec.InstanceID)
	case contracts.TaskUnsuspend:
		return h.Start(task.Spec.InstanceID)
	default:
		return fmt.Errorf("unknown task type: %s", task.Type)
	}
}
