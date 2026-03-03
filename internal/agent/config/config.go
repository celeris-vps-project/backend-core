package config

// Config holds the agent's runtime configuration.
type Config struct {
	NodeID        string            `json:"node_id"`
	Secret        string            `json:"secret"`
	ControllerURL string            `json:"controller_url"` // e.g. "http://controller:8888"
	PollInterval  int               `json:"poll_interval"`  // seconds between heartbeats
	VirtBackend   string            `json:"virt_backend"`   // "libvirt", "incus", or "stub"
	VirtOpts      map[string]string `json:"virt_opts"`      // backend-specific: {"uri":"qemu:///system"} or {"project":"default"}
}

// DefaultConfig returns the default configuration for the agent.
func DefaultConfig() Config {
	return Config{
		ControllerURL: "http://127.0.0.1:8888",
		PollInterval:  15,
		VirtBackend:   "stub",
		VirtOpts:      map[string]string{},
	}
}
