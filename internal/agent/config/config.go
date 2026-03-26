package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// NATConfig holds NAT-specific configuration for the agent.
type NATConfig struct {
	SSHTargetPort   int    `json:"ssh_target_port" yaml:"ssh_target_port"`     // VM internal SSH port (default: 22)
	InternalNetwork string `json:"internal_network" yaml:"internal_network"`   // NAT internal network CIDR (e.g. "10.0.0.0/24")
}

// Config holds the agent's runtime configuration.
type Config struct {
	BootstrapToken string            `json:"bootstrap_token" yaml:"bootstrap_token"` // one-time bootstrap token for initial registration
	CredentialFile string            `json:"credential_file" yaml:"credential_file"` // path to save/load the permanent node credential
	ControllerURL  string            `json:"controller_url" yaml:"controller_url"`   // e.g. "http://controller:8888" (legacy HTTP)
	GRPCAddress    string            `json:"grpc_address" yaml:"grpc_address"`       // e.g. "controller:50051"
	PollInterval   int               `json:"poll_interval" yaml:"poll_interval"`     // seconds between heartbeats
	VirtBackend    string            `json:"virt_backend" yaml:"virt_backend"`       // "libvirt", "incus", or "stub"
	VirtOpts       map[string]string `json:"virt_opts" yaml:"virt_opts"`             // backend-specific: {"uri":"qemu:///system"} or {"project":"default"}
	NAT            NATConfig         `json:"nat" yaml:"nat"`                         // NAT mode configuration
}

// DefaultConfig returns the default configuration for the agent.
func DefaultConfig() Config {
	return Config{
		ControllerURL:  "http://127.0.0.1:8888",
		GRPCAddress:    "127.0.0.1:50051",
		PollInterval:   15,
		CredentialFile: "node-credential.yaml",
		VirtBackend:    "stub",
		VirtOpts:       map[string]string{},
		NAT: NATConfig{
			SSHTargetPort:   22,
			InternalNetwork: "10.0.0.0/24",
		},
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
