package contracts

// AgentRegistration is sent by the agent on first connect.
// The agent does NOT declare its own NodeID, location, or capacity —
// all of that is configured in the admin panel and resolved server-side
// via the bootstrap token's binding.
type AgentRegistration struct {
	BootstrapToken string `json:"bootstrap_token"` // one-time bootstrap token
	Hostname       string `json:"hostname"`        // informational only; stored in runtime state
	IP             string `json:"ip"`
	Version        string `json:"version"`
}

// RegistrationResult is returned by the controller after a successful bootstrap registration.
type RegistrationResult struct {
	NodeID    string `json:"node_id"`    // the host node ID that this agent is now bound to
	NodeToken string `json:"node_token"` // permanent node credential for subsequent requests
}

// Heartbeat is sent periodically by the agent to report health.
type Heartbeat struct {
	NodeID     string                 `json:"node_id"`
	CPUUsage   float64                `json:"cpu_usage"`
	MemUsage   float64                `json:"mem_usage"`
	DiskUsage  float64                `json:"disk_usage"`
	Uptime     int64                  `json:"uptime"`
	VMCount    int                    `json:"vm_count"`
	ReportedAt string                 `json:"reported_at"`
	VMStates   []InstanceRuntimeState `json:"vm_states,omitempty"`
}

// VMTransferred describe traffic consuming stats
type VMTransferred struct {
	Total uint64 `json:"total"`
	RX    uint64 `json:"rx"`
	TX    uint64 `json:"tx"`
}

// InstanceRuntimeState is agent-reported hypervisor state for a single guest.
// It is runtime telemetry and should live in caches, not in the instances table.
type InstanceRuntimeState struct {
	InstanceID    string        `json:"instance_id"`
	State         string        `json:"state"` // "running", "stopped", "paused", "unknown"
	IPv4          string        `json:"ipv4,omitempty"`
	IPv6          string        `json:"ipv6,omitempty"`
	ReportedAt    string        `json:"reported_at,omitempty"`
	VMTransferred VMTransferred `json:"vm_transferred,omitempty"`
}

// HeartbeatAck is the controller's response, optionally including queued tasks.
type HeartbeatAck struct {
	OK              bool             `json:"ok"`
	Tasks           []Task           `json:"tasks,omitempty"`
	NATForwards     []NATForwardRule `json:"nat_forwards,omitempty"`
	ConsoleSessions []ConsoleSession `json:"console_sessions,omitempty"`
}

type ConsoleSession struct {
	SessionID  string `json:"session_id"`
	InstanceID string `json:"instance_id"`
}

type ConsoleFrame struct {
	SessionID  string `json:"session_id"`
	InstanceID string `json:"instance_id,omitempty"`
	Data       []byte `json:"data,omitempty"`
	Error      string `json:"error,omitempty"`
	Control    string `json:"control,omitempty"`
}

type ConsoleStream interface {
	Send(ConsoleFrame) error
	Recv() (ConsoleFrame, error)
}
