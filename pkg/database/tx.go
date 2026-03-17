// Package database — TransactionManager extension.
//
// TransactionManager provides a clean abstraction for wrapping multi-step
// business operations in database transactions. This prevents partial writes
// when a sequence of repo calls must succeed or fail as a unit.
//
// The transaction is propagated via context.Context, so repositories can
// automatically use the transactional connection when one is active, or
// fall back to the default connection otherwise.
//
// Usage:
//
//	txMgr := database.NewTransactionManager(db)
//	err := txMgr.RunInTransaction(ctx, func(txCtx context.Context) error {
//	    if err := repo1.Save(txCtx, entity1); err != nil {
//	        return err // triggers rollback
//	    }
//	    if err := repo2.Save(txCtx, entity2); err != nil {
//	        return err // triggers rollback
//	    }
//	    return nil // triggers commit
//	})
package database

import (
	"context"
	"log"

	"gorm.io/gorm"
)

// ctxKey is the context key type for storing the transactional *gorm.DB.
type ctxKey struct{}

// TransactionManager wraps GORM's transaction support with context propagation.
type TransactionManager struct {
	db *gorm.DB
}

// NewTransactionManager creates a transaction manager for the given database.
func NewTransactionManager(db *gorm.DB) *TransactionManager {
	log.Printf("[database] transaction manager initialised")
	return &TransactionManager{db: db}
}

// RunInTransaction executes fn within a database transaction.
//
// The transactional *gorm.DB is stored in the returned context, which fn
// receives as its parameter. Repositories should use ExtractDB(ctx, defaultDB)
// to obtain the correct connection.
//
// If fn returns nil, the transaction is committed.
// If fn returns an error (or panics), the transaction is rolled back.
func (m *TransactionManager) RunInTransaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := context.WithValue(ctx, ctxKey{}, tx)
		return fn(txCtx)
	})
}

// ExtractDB returns the transactional *gorm.DB from the context if one is
// active, otherwise returns the provided default *gorm.DB.
//
// This is the key function that repositories use to transparently participate
// in transactions without changing their method signatures:
//
//	func (r *GormOrderRepo) Save(ctx context.Context, order *domain.Order) error {
//	    db := database.ExtractDB(ctx, r.db)
//	    return db.Save(&po).Error
//	}
//
// When called outside a transaction, ExtractDB returns r.db (normal connection).
// When called inside RunInTransaction, ExtractDB returns the tx connection.
func ExtractDB(ctx context.Context, defaultDB *gorm.DB) *gorm.DB {
	if ctx == nil {
		return defaultDB
	}
	if tx, ok := ctx.Value(ctxKey{}).(*gorm.DB); ok {
		return tx
	}
	return defaultDB
}
