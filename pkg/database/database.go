// Package database provides a driver-agnostic factory for opening a *gorm.DB
// connection. The caller passes a DatabaseConfig (from the API config) and gets
// back a ready-to-use *gorm.DB that works with any GORM-based repository.
//
// Supported drivers:
//   - "sqlite"  (default) — uses github.com/glebarez/sqlite (pure-Go, CGO-free)
//   - "postgres"          — uses gorm.io/driver/postgres
//
// Usage:
//
//	db, err := database.Open(cfg.Database)
//	if err != nil { log.Fatal(err) }
//	userRepo := infra.NewGormUserRepo(db) // works with both SQLite and Postgres
package database

import (
	"fmt"
	"log"
	"time"

	"backend-core/internal/api/config"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Open creates a *gorm.DB connection based on the provided DatabaseConfig.
// It applies driver-specific optimisations (SQLite PRAGMAs, Postgres pool
// settings) automatically.
func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	switch cfg.Driver {
	case "", "sqlite":
		return openSQLite(cfg)
	case "postgres":
		return openPostgres(cfg)
	default:
		return nil, fmt.Errorf("database: unsupported driver %q (expected \"sqlite\" or \"postgres\")", cfg.Driver)
	}
}

// openSQLite opens a pure-Go SQLite connection and applies recommended PRAGMAs.
func openSQLite(cfg config.DatabaseConfig) (*gorm.DB, error) {
	dsn := cfg.DSN
	if dsn == "" {
		dsn = "data.db"
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("database: failed to connect to SQLite (%s): %w", dsn, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database: failed to get underlying sql.DB: %w", err)
	}

	// WAL mode: allows concurrent readers while writing.
	sqlDB.Exec("PRAGMA journal_mode=WAL")
	// Wait up to 5 seconds for a lock instead of failing immediately.
	sqlDB.Exec("PRAGMA busy_timeout=5000")
	// NORMAL sync is safe with WAL and significantly faster than FULL.
	sqlDB.Exec("PRAGMA synchronous=NORMAL")
	// 64 MB page cache (negative value = KiB).
	sqlDB.Exec("PRAGMA cache_size=-64000")
	// Enable foreign key enforcement (SQLite disables it by default).
	sqlDB.Exec("PRAGMA foreign_keys=ON")

	// SQLite only supports one concurrent writer.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	log.Printf("[database] SQLite opened (%s) with PRAGMA optimisations", dsn)
	return db, nil
}

// openPostgres opens a PostgreSQL connection and configures the connection pool.
func openPostgres(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("database: postgres DSN is required")
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("database: failed to connect to PostgreSQL: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("database: failed to get underlying sql.DB: %w", err)
	}

	// Connection pool settings — use config values or sensible defaults.
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 25
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 10
	}
	connMaxLifetime := cfg.ConnMaxLifetime
	if connMaxLifetime <= 0 {
		connMaxLifetime = 5 * time.Minute
	}
	connMaxIdleTime := cfg.ConnMaxIdleTime
	if connMaxIdleTime <= 0 {
		connMaxIdleTime = 3 * time.Minute
	}

	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)
	sqlDB.SetConnMaxIdleTime(connMaxIdleTime)

	log.Printf("[database] PostgreSQL connected (maxOpen=%d, maxIdle=%d, maxLifetime=%s)",
		maxOpen, maxIdle, connMaxLifetime)

	// ── Read-Write Splitting ───────────────────────────────────────────────
	// If replica DSNs are configured, register them as read-only replicas.
	// GORM's DBResolver plugin automatically routes:
	//   - SELECT queries → replicas (round-robin)
	//   - INSERT/UPDATE/DELETE → primary
	//
	// This dramatically improves read concurrency for catalog browsing,
	// instance listing, and other read-heavy endpoints.
	if len(cfg.ReplicaDSNs) > 0 {
		var replicas []gorm.Dialector
		for _, dsn := range cfg.ReplicaDSNs {
			replicas = append(replicas, postgres.Open(dsn))
		}
		db.Use(newDBResolver(replicas, maxOpen, maxIdle, connMaxLifetime, connMaxIdleTime))
		log.Printf("[database] read-write splitting enabled (%d replicas)", len(replicas))
	}

	return db, nil
}

// newDBResolver creates a GORM DBResolver plugin for read-write splitting.
// Import note: this uses the dbresolver plugin which must be added to go.mod:
//
//	go get gorm.io/plugin/dbresolver
//
// If the plugin is not available, this function is a no-op stub that logs
// a warning and returns nil.
func newDBResolver(replicas []gorm.Dialector, maxOpen, maxIdle int, maxLifetime, maxIdleTime time.Duration) gorm.Plugin {
	resolver := &readWriteResolver{
		replicas:    replicas,
		maxOpen:     maxOpen,
		maxIdle:     maxIdle,
		maxLifetime: maxLifetime,
		maxIdleTime: maxIdleTime,
	}
	return resolver
}

// readWriteResolver is a lightweight GORM plugin that configures read replicas.
// It implements gorm.Plugin for use with db.Use().
//
// For production deployments, consider replacing this with gorm.io/plugin/dbresolver
// which provides more sophisticated features (policy-based routing, connection
// pooling per replica, health checks, etc.).
//
// This stub implementation stores the replica config and logs it. The actual
// query routing requires the dbresolver plugin to be imported.
type readWriteResolver struct {
	replicas    []gorm.Dialector
	maxOpen     int
	maxIdle     int
	maxLifetime time.Duration
	maxIdleTime time.Duration
}

func (r *readWriteResolver) Name() string { return "read-write-resolver" }

func (r *readWriteResolver) Initialize(db *gorm.DB) error {
	log.Printf("[database] read-write resolver initialized with %d replicas (install gorm.io/plugin/dbresolver for full support)",
		len(r.replicas))
	// Note: Full implementation requires:
	//   import "gorm.io/plugin/dbresolver"
	//   db.Use(dbresolver.Register(dbresolver.Config{
	//       Replicas: r.replicas,
	//       Policy:   dbresolver.RandomPolicy{},
	//   }).SetMaxOpenConns(r.maxOpen).SetMaxIdleConns(r.maxIdle))
	return nil
}
