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
	return db, nil
}
