package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the API server's runtime configuration.
type Config struct {
	Database  DatabaseConfig       `json:"database" yaml:"database"`
	JWT       JWTConfig            `json:"jwt" yaml:"jwt"`
	GRPC      GRPCConfig           `json:"grpc" yaml:"grpc"`
	RateLimit RateLimitConfig      `json:"rate_limit" yaml:"rate_limit"`
	Crypto    CryptoPaymentConfig  `json:"crypto" yaml:"crypto"`
}

// DatabaseConfig holds database connection settings.
type DatabaseConfig struct {
	Driver string `json:"driver" yaml:"driver"` // "sqlite" (default) or "postgres"
	DSN    string `json:"dsn" yaml:"dsn"`       // e.g. "data.db" for SQLite, or PG connection string

	// PostgreSQL connection pool settings (ignored for SQLite).
	MaxOpenConns    int           `json:"max_open_conns" yaml:"max_open_conns"`         // default: 25
	MaxIdleConns    int           `json:"max_idle_conns" yaml:"max_idle_conns"`         // default: 10
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime" yaml:"conn_max_lifetime"`   // default: 5m
	ConnMaxIdleTime time.Duration `json:"conn_max_idle_time" yaml:"conn_max_idle_time"` // default: 3m

	// Read replica DSNs for read-write splitting (PostgreSQL only).
	// When configured, SELECT queries are automatically routed to replicas
	// while INSERT/UPDATE/DELETE go to the primary. This distributes read
	// load across replicas, significantly improving concurrency for
	// read-heavy workloads (catalog browsing, instance listing, etc.).
	//
	// Leave empty to use a single primary for all operations.
	ReplicaDSNs []string `json:"replica_dsns" yaml:"replica_dsns"`
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

// RateLimitTier holds rate limit settings for a specific tier of endpoints.
type RateLimitTier struct {
	// GlobalQPS is the maximum requests per second allowed across ALL clients
	// hitting endpoints in this tier. 0 means unlimited.
	GlobalQPS float64 `json:"global_qps" yaml:"global_qps"`

	// IPMaxQPS is the maximum requests per second allowed per unique client IP.
	// 0 means unlimited.
	IPMaxQPS float64 `json:"ip_max_qps" yaml:"ip_max_qps"`
}

// RateLimitConfig holds tiered token-bucket rate limiting settings.
//
// Each tier represents a group of endpoints with similar traffic patterns
// and protection requirements:
//
//   - Baseline: a safety-net global limiter applied to ALL endpoints (very
//     permissive). Prevents total resource exhaustion even on unclassified
//     routes. Set GlobalQPS=0 to disable the baseline entirely.
//
//   - Critical: high-traffic read endpoints (product catalog, groups, regions).
//     Higher global QPS to handle legitimate traffic; moderate per-IP to block
//     crawlers and scrapers.
//
//   - Checkout: purchase/payment write endpoints that involve inventory or
//     funds. Strict per-IP limiting to prevent automated abuse.
//
//   - Auth: login and registration endpoints. Very strict per-IP to block
//     brute-force and credential-stuffing attacks.
//
//   - Standard: general authenticated business endpoints (orders, invoices,
//     instances CRUD). Moderate protection.
//
//   - Admin: admin-only endpoints already protected by RBAC. Relaxed limits.
type RateLimitConfig struct {
	// Baseline is a loose global safety-net applied to ALL endpoints.
	// It should be very permissive (e.g. 5000 QPS global) — it only
	// fires if something truly anomalous is happening.
	Baseline RateLimitTier `json:"baseline" yaml:"baseline"`

	// Critical — public catalog GET endpoints (products, groups, regions,
	// resource-pools). High throughput allowed, moderate per-IP.
	Critical RateLimitTier `json:"critical" yaml:"critical"`

	// Checkout — POST /checkout, POST /products/purchase, POST /orders/:id/pay.
	// Strict per-IP to prevent automated purchase abuse.
	Checkout RateLimitTier `json:"checkout" yaml:"checkout"`

	// Auth — POST /auth/login, POST /auth/register.
	// Very strict per-IP to prevent brute-force attacks.
	Auth RateLimitTier `json:"auth" yaml:"auth"`

	// Standard — general authenticated endpoints (invoices, orders, instances).
	// Moderate protection for normal business operations.
	Standard RateLimitTier `json:"standard" yaml:"standard"`

	// Admin — admin-only endpoints. Already protected by RBAC middleware,
	// so rate limiting is relaxed.
	Admin RateLimitTier `json:"admin" yaml:"admin"`
}

// CryptoPaymentConfig holds USDT crypto payment settings.
//
// The payment module supports two modes:
//
//   - MockMode (development): charges are auto-confirmed after MockConfirmDelay.
//     No real blockchain interaction. Good for local development and testing.
//
//   - Production mode (mock_mode: false): the system waits for a real webhook
//     callback from a blockchain monitoring service to confirm on-chain payments.
//     Configure Wallets with your actual receiving addresses per network.
//
// Supported networks: arbitrum, solana, trc20, bsc, polygon
//
// Environment variable override: CRYPTO_MOCK_MODE=false disables mock mode.
type CryptoPaymentConfig struct {
	// MockMode: true = auto-confirm payments after MockConfirmDelay (dev/testing).
	// false = wait for real blockchain confirmation via webhook (production).
	MockMode bool `json:"mock_mode" yaml:"mock_mode"`

	// MockConfirmDelay is the delay before auto-confirming in mock mode.
	// Parsed as Go duration string (e.g. "3s", "500ms"). Default: "3s".
	MockConfirmDelay string `json:"mock_confirm_delay" yaml:"mock_confirm_delay"`

	// PaymentTimeout is how long a charge remains valid before expiring.
	// Parsed as Go duration string (e.g. "30m", "1h"). Default: "30m".
	PaymentTimeout string `json:"payment_timeout" yaml:"payment_timeout"`

	// Wallets maps each blockchain network to the receiving wallet address.
	// Keys: "arbitrum", "solana", "trc20", "bsc", "polygon"
	// In production, replace these with your actual receiving addresses.
	Wallets map[string]string `json:"wallets" yaml:"wallets"`
}

// DefaultConfig returns sensible defaults for development.
func DefaultConfig() Config {
	return Config{
		Database: DatabaseConfig{
			Driver: "sqlite",
			DSN:    "data.db",
		},
		JWT: JWTConfig{
			Secret: "my-super-secret-key",
			Issuer: "celeris-api",
		},
		GRPC: GRPCConfig{
			Listen: ":50051",
		},
		RateLimit: RateLimitConfig{
			Baseline: RateLimitTier{GlobalQPS: 5000, IPMaxQPS: 50},
			Critical: RateLimitTier{GlobalQPS: 2000, IPMaxQPS: 30},
			Checkout: RateLimitTier{GlobalQPS: 1000, IPMaxQPS: 5},
			Auth:     RateLimitTier{GlobalQPS: 500, IPMaxQPS: 3},
			Standard: RateLimitTier{GlobalQPS: 1000, IPMaxQPS: 15},
			Admin:    RateLimitTier{GlobalQPS: 0, IPMaxQPS: 20},
		},
		Crypto: CryptoPaymentConfig{
			MockMode:         true,
			MockConfirmDelay: "3s",
			PaymentTimeout:   "30m",
			Wallets: map[string]string{
				"arbitrum": "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD3E",
				"solana":   "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
				"trc20":    "TN3W4H6rK2ce4vX9YnFQHwKENnHjoxb3m9",
				"bsc":      "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD3E",
				"polygon":  "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD3E",
			},
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
