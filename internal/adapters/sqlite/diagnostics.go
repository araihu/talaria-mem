package sqlite

import (
	"context"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type FTSReport struct {
	Tokenizer    string
	SecureDelete bool
	Rows         int64
}

func (database *DB) FTSReport(ctx context.Context) (FTSReport, error) {
	if database == nil || database.sql == nil {
		return FTSReport{}, domain.NewError(domain.CodeUnavailable, "SQLite diagnostics unavailable", true)
	}
	var definition string
	if err := database.sql.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name = 'memory_fts'`).Scan(&definition); err != nil {
		return FTSReport{}, domain.MapSQLiteError(err)
	}
	var rows int64
	if err := database.sql.QueryRowContext(ctx, `SELECT count(*) FROM memory_fts`).Scan(&rows); err != nil {
		return FTSReport{}, domain.MapSQLiteError(err)
	}
	return FTSReport{Tokenizer: tokenizerFromDefinition(definition), SecureDelete: true, Rows: rows}, nil
}

func (database *DB) RepairFTS(ctx context.Context) error {
	if database == nil || database.sql == nil {
		return domain.NewError(domain.CodeUnavailable, "SQLite diagnostics unavailable", true)
	}
	return NewRepository(database).RebuildFTS(ctx)
}

func (database *DB) FreelistPages(ctx context.Context) (int64, error) {
	if database == nil || database.sql == nil {
		return 0, domain.NewError(domain.CodeUnavailable, "SQLite diagnostics unavailable", true)
	}
	var pages int64
	if err := database.sql.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&pages); err != nil {
		return 0, domain.MapSQLiteError(err)
	}
	return pages, nil
}

func tokenizerFromDefinition(definition string) string {
	definition = strings.ToLower(definition)
	const marker = "tokenize='"
	index := strings.Index(definition, marker)
	if index < 0 {
		return ""
	}
	value := definition[index+len(marker):]
	if end := strings.IndexByte(value, '\''); end >= 0 {
		return value[:end]
	}
	return value
}
