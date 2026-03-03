package contracts

// AgentRegistration is sent by the agent on first connect.
type AgentRegistration struct {
	NodeID   string `json:"node_id"`
	Secret   string `json:"secret"`
	Hostname string `json:"hostname"`
	Location string `json:"location"` // e.g. "DE-fra"; used when auto-registering a new node
	IP       string `json:"ip"`
	Version  string `json:"version"`
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
