package sqlite

import (
	"context"
	"database/sql"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

func (database *DB) WithTx(ctx context.Context, operation func(*sql.Tx) error) (err error) {
	tx, err := database.sql.BeginTx(ctx, nil)
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = operation(tx); err != nil {
		return domain.MapSQLiteError(err)
	}
	if database.beforeCommit != nil {
		if err = database.beforeCommit(); err != nil {
			return domain.MapSQLiteError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.MapSQLiteError(err)
	}
	return nil
}
