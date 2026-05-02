package database

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpsertByPrimaryKey inserts po or updates the listed columns when the primary
// key already exists. It avoids GORM Save's implicit update-then-insert behavior,
// which is harder to reason about across SQL dialects.
func UpsertByPrimaryKey(db *gorm.DB, po any, columns []string) error {
	return db.Clauses(primaryKeyUpsertClause(columns)).Create(po).Error
}

func primaryKeyUpsertClause(columns []string) clause.OnConflict {
	return clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns(columns),
	}
}
