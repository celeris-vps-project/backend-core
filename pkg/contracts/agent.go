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
	NodeID     string  `json:"node_id"`
	CPUUsage   float64 `json:"cpu_usage"`
	MemUsage   float64 `json:"mem_usage"`
	DiskUsage  float64 `json:"disk_usage"`
	Uptime     int64   `json:"uptime"`
	VMCount    int     `json:"vm_count"`
	ReportedAt string  `json:"reported_at"`
}

// HeartbeatAck is the controller's response, optionally including queued tasks.
type HeartbeatAck struct {
	OK    bool   `json:"ok"`
	Tasks []Task `json:"tasks,omitempty"`
}
