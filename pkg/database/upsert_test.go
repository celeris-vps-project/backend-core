package database

import (
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type upsertDryRunPO struct {
	ID   string `gorm:"primaryKey;column:id"`
	Name string `gorm:"column:name"`
}

func (upsertDryRunPO) TableName() string { return "upsert_dry_run" }

func TestUpsertByPrimaryKey_PostgresDryRun(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  "host=127.0.0.1 user=celeris password=celeris dbname=celeris port=5432 sslmode=disable",
		PreferSimpleProtocol: true,
		WithoutReturning:     true,
	}), &gorm.Config{
		DryRun:               true,
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatalf("open dry-run postgres: %v", err)
	}

	sql := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
		return tx.Clauses(primaryKeyUpsertClause([]string{"name"})).
			Create(&upsertDryRunPO{ID: "id-1", Name: "node-1"})
	})
	for _, want := range []string{
		`INSERT INTO "upsert_dry_run"`,
		`ON CONFLICT ("id") DO UPDATE`,
		`"name"="excluded"."name"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("expected SQL to contain %q, got %s", want, sql)
		}
	}
}
