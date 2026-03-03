package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the agent's runtime configuration.
type Config struct {
	NodeID        string            `json:"node_id" yaml:"node_id"`
	Secret        string            `json:"secret" yaml:"secret"`
	Location      string            `json:"location" yaml:"location"`             // e.g. "DE-fra"; reported during registration
	ControllerURL string            `json:"controller_url" yaml:"controller_url"` // e.g. "http://controller:8888" (legacy HTTP)
	GRPCAddress   string            `json:"grpc_address" yaml:"grpc_address"`     // e.g. "controller:50051"
	PollInterval  int               `json:"poll_interval" yaml:"poll_interval"`   // seconds between heartbeats
	VirtBackend   string            `json:"virt_backend" yaml:"virt_backend"`     // "libvirt", "incus", or "stub"
	VirtOpts      map[string]string `json:"virt_opts" yaml:"virt_opts"`           // backend-specific: {"uri":"qemu:///system"} or {"project":"default"}
}

// DefaultConfig returns the default configuration for the agent.
func DefaultConfig() Config {
	return Config{
		ControllerURL: "http://127.0.0.1:8888",
		GRPCAddress:   "127.0.0.1:50051",
		PollInterval:  15,
		VirtBackend:   "stub",
		VirtOpts:      map[string]string{},
	}
}

// LoadFromFile reads a YAML config file and returns a Config.
// Default values are applied first, then overridden by the file contents.
func LoadFromFile(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read config file %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}
	return cfg, nil
}
