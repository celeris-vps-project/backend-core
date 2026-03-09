package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the API server's runtime configuration.
type Config struct {
	Database DatabaseConfig `json:"database" yaml:"database"`
	JWT      JWTConfig      `json:"jwt" yaml:"jwt"`
	GRPC     GRPCConfig     `json:"grpc" yaml:"grpc"`
}

// DatabaseConfig holds database connection settings.
type DatabaseConfig struct {
	DSN string `json:"dsn" yaml:"dsn"` // e.g. "data.db" for SQLite
}

// JWTConfig holds JWT signing settings.
type JWTConfig struct {
	Secret string `json:"secret" yaml:"secret"`
	Issuer string `json:"issuer" yaml:"issuer"`
}

// GRPCConfig holds gRPC server settings.
type GRPCConfig struct {
	Listen string `json:"listen" yaml:"listen"` // e.g. ":50051"
}

// DefaultConfig returns sensible defaults for development.
func DefaultConfig() Config {
	return Config{
		Database: DatabaseConfig{
			DSN: "data.db",
		},
		JWT: JWTConfig{
			Secret: "my-super-secret-key",
			Issuer: "celeris-api",
		},
		GRPC: GRPCConfig{
			Listen: ":50051",
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
