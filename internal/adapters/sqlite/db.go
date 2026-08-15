package sqlite

import (
	"context"
	"database/sql"

	_ "modernc.org/sqlite"
)

// DB is the T3 RED adapter shell. It creates a deliberately lax journal table.
type DB struct {
	sql *sql.DB
}

func Open(ctx context.Context, path string) (*DB, error) {
	handle, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return nil, err
	}
	if _, err := handle.ExecContext(ctx, `CREATE TABLE rule_activation_journal (
		id INTEGER PRIMARY KEY,
		phase TEXT NOT NULL,
		live_mutation_started INTEGER NOT NULL
	)`); err != nil {
		_ = handle.Close()
		return nil, err
	}
	return &DB{sql: handle}, nil
}

func (database *DB) SQL() *sql.DB { return database.sql }

func (database *DB) Close() error { return database.sql.Close() }

func ApplyMigrations(context.Context, *sql.DB) error { return nil }
