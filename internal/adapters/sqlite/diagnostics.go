package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type FTSReport struct {
	Tokenizer    string
	SecureDelete bool
	Rows         int64
	ExpectedRows int64
	ExpectedHash string
	ActualHash   string
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
	var secureDelete int64
	if err := database.sql.QueryRowContext(ctx, `SELECT v FROM memory_fts_config WHERE k = 'secure-delete'`).Scan(&secureDelete); err != nil {
		return FTSReport{}, domain.MapSQLiteError(err)
	}
	expectedRows, expectedHash, err := database.ftsDigest(ctx, `
SELECT memories.id, revisions.title, revisions.content, revisions.tags_json
FROM memories
JOIN memory_revisions AS revisions ON revisions.id = memories.current_revision_id
WHERE memories.trust IN ('verified', 'generated')
  AND memories.lifecycle = 'active'
  AND revisions.trust IN ('verified', 'generated')
  AND revisions.lifecycle = 'active'
ORDER BY memories.id`)
	if err != nil {
		return FTSReport{}, err
	}
	_, actualHash, err := database.ftsDigest(ctx, `
SELECT memory_id, title, content, tags
FROM memory_fts
ORDER BY memory_id`)
	if err != nil {
		return FTSReport{}, err
	}
	return FTSReport{Tokenizer: tokenizerFromDefinition(definition), SecureDelete: secureDelete == 1, Rows: rows, ExpectedRows: expectedRows, ExpectedHash: expectedHash, ActualHash: actualHash}, nil
}

// FTSIntegrity returns a deterministic metadata-only comparison of canonical
// eligible rows and the materialized FTS table. Content never leaves this
// adapter; only row counts and SHA-256 digests cross the diagnostic boundary.
func (database *DB) FTSIntegrity(ctx context.Context) (FTSReport, error) {
	return database.FTSReport(ctx)
}

func (database *DB) ftsDigest(ctx context.Context, query string) (int64, string, error) {
	rows, err := database.sql.QueryContext(ctx, query)
	if err != nil {
		return 0, "", domain.MapSQLiteError(err)
	}
	defer rows.Close()
	hash := sha256.New()
	var count int64
	for rows.Next() {
		var memoryID, title, content, tags string
		if err := rows.Scan(&memoryID, &title, &content, &tags); err != nil {
			return 0, "", domain.MapSQLiteError(err)
		}
		count++
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\n", memoryID, title, content, tags)
	}
	if err := rows.Err(); err != nil {
		return 0, "", domain.MapSQLiteError(err)
	}
	return count, hex.EncodeToString(hash.Sum(nil)), nil
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
