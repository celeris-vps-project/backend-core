package contracts

// TaskType enumerates the provisioning commands the controller can send.
type TaskType string

const (
	TaskProvision   TaskType = "provision"
	TaskDeprovision TaskType = "deprovision"
	TaskStart       TaskType = "start"
	TaskStop        TaskType = "stop"
	TaskReboot      TaskType = "reboot"
	TaskSuspend     TaskType = "suspend"
	TaskUnsuspend   TaskType = "unsuspend"
)

// TaskStatus tracks a task through its lifecycle.
type TaskStatus string

const (
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
)

// VirtType selects the virtualisation technology.
type VirtType string

const (
	VirtKVM VirtType = "kvm"
	VirtLXC VirtType = "lxc"
)

// NetworkMode selects the network allocation strategy.
type NetworkMode string

const (
	NetworkModeDedicated NetworkMode = "dedicated" // one public IP per instance
	NetworkModeNAT       NetworkMode = "nat"       // shared host IP + port mapping
)

// ProvisionSpec describes the VM/container that should be created on the host.
type ProvisionSpec struct {
	InstanceID  string   `json:"instance_id"`
	Hostname    string   `json:"hostname"`
	OS          string   `json:"os"` // image name or path, e.g. "ubuntu-22.04"
	CPU         int      `json:"cpu"`
	MemoryMB    int      `json:"memory_mb"`
	DiskGB      int      `json:"disk_gb"`
	IPv4        string   `json:"ipv4,omitempty"`
	IPv6        string   `json:"ipv6,omitempty"`
	VirtType    VirtType `json:"virt_type"`              // "kvm" or "lxc"
	StoragePool string   `json:"storage_pool,omitempty"` // e.g. "default", "zfs-pool"
	NetworkName string   `json:"network_name,omitempty"` // e.g. "br0", "default"
	SSHKeys     []string `json:"ssh_keys,omitempty"`

	// NAT mode fields (only set when NetworkMode == "nat")
	NetworkMode NetworkMode `json:"network_mode,omitempty"` // "dedicated" or "nat"; empty = dedicated
	NATPort     int         `json:"nat_port,omitempty"`     // high port on host mapped to VM SSH (e.g. 20001)
}

// Task is a unit of work sent from the controller to an agent.
type Task struct {
	ID         string        `json:"id"`
	NodeID     string        `json:"node_id"`
	Type       TaskType      `json:"type"`
	Status     TaskStatus    `json:"status"`
	Spec       ProvisionSpec `json:"spec"`
	Error      string        `json:"error,omitempty"`
	CreatedAt  string        `json:"created_at"`
	FinishedAt string        `json:"finished_at,omitempty"`
}

// TaskResult is the payload the agent sends back to the controller.
type TaskResult struct {
	TaskID     string     `json:"task_id"`
	Status     TaskStatus `json:"status"`
	Error      string     `json:"error,omitempty"`
	IPv4       string     `json:"ipv4,omitempty"`
	IPv6       string     `json:"ipv6,omitempty"`
	VMState    string     `json:"vm_state,omitempty"`    // current VM state: "running", "stopped", "boot_timeout"
	FinishedAt string     `json:"finished_at"`
}
