package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/guilhermecastro/talaria-mem/db"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	_ "modernc.org/sqlite"
)

type DB struct {
	sql          *sql.DB
	beforeCommit func() error
}

func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, domain.NewError(domain.CodeValidation, "database path is required", false)
	}
	if err := prepareDatabaseFile(path); err != nil {
		return nil, err
	}

	dsn := databaseDSN(path)
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, domain.MapSQLiteError(err)
	}
	handle.SetMaxOpenConns(domain.MaxConcurrentReads + domain.MaxConcurrentWriters)
	handle.SetMaxIdleConns(domain.MaxConcurrentReads + domain.MaxConcurrentWriters)

	database := &DB{sql: handle}
	if err := handle.PingContext(ctx); err != nil {
		handle.Close()
		return nil, domain.MapSQLiteError(err)
	}
	if err := ApplyMigrations(ctx, handle); err != nil {
		handle.Close()
		return nil, err
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		handle.Close()
		return nil, domain.MapSQLiteError(err)
	}
	if err := verifyDatabaseConfiguration(ctx, handle); err != nil {
		handle.Close()
		return nil, err
	}
	return database, nil
}

func prepareDatabaseFile(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return domain.NewError(domain.CodeValidation, "unsafe database path", false)
		}
		if info.Mode().Perm() != 0o600 {
			return domain.NewError(domain.CodeValidation, "unsafe database permissions", false)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func databaseDSN(path string) string {
	dsn := &url.URL{Scheme: "file", Path: path}
	query := dsn.Query()
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "secure_delete(ON)")
	query.Add("_pragma", "busy_timeout(5000)")
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

func verifyDatabaseConfiguration(ctx context.Context, handle *sql.DB) error {
	checks := []struct {
		pragma string
		want   int
	}{
		{pragma: "foreign_keys", want: 1},
		{pragma: "secure_delete", want: 1},
		{pragma: "auto_vacuum", want: 2},
	}
	for _, check := range checks {
		var got int
		if err := handle.QueryRowContext(ctx, "PRAGMA "+check.pragma).Scan(&got); err != nil {
			return domain.MapSQLiteError(err)
		}
		if got != check.want {
			return fmt.Errorf("sqlite configuration drift: %s=%d", check.pragma, got)
		}
	}
	var journalMode string
	if err := handle.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return domain.MapSQLiteError(err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("sqlite configuration drift: journal_mode=%s", journalMode)
	}
	var schema string
	if err := handle.QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'memory_fts'",
	).Scan(&schema); err != nil {
		return domain.MapSQLiteError(err)
	}
	if !strings.Contains(schema, "tokenize='"+domain.FTS5Tokenizer+"'") {
		return domain.NewError(domain.CodeUnavailable, "FTS tokenizer configuration drift", false)
	}
	var ftsSecureDelete int
	if err := handle.QueryRowContext(ctx,
		"SELECT v FROM memory_fts_config WHERE k = 'secure-delete'",
	).Scan(&ftsSecureDelete); err != nil {
		return domain.MapSQLiteError(err)
	}
	if ftsSecureDelete != 1 {
		return domain.NewError(domain.CodeUnavailable, "FTS secure-delete configuration drift", false)
	}
	return nil
}

func ApplyMigrations(ctx context.Context, handle *sql.DB) error {
	return applyMigrationDirection(ctx, handle, ".up.sql", false)
}

func ApplyDownMigrations(ctx context.Context, handle *sql.DB) error {
	return applyMigrationDirection(ctx, handle, ".down.sql", true)
}

func applyMigrationDirection(ctx context.Context, handle *sql.DB, suffix string, reverse bool) error {
	entries, err := fs.ReadDir(db.Migrations, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if reverse {
		for left, right := 0, len(names)-1; left < right; left, right = left+1, right-1 {
			names[left], names[right] = names[right], names[left]
		}
	}
	var currentVersion int
	if err := handle.QueryRowContext(ctx, "PRAGMA user_version").Scan(&currentVersion); err != nil {
		return domain.MapSQLiteError(err)
	}
	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("parse migration version %s: %w", name, err)
		}
		if (!reverse && version <= currentVersion) || (reverse && version > currentVersion) {
			continue
		}
		contents, err := db.Migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := handle.BeginTx(ctx, nil)
		if err != nil {
			return domain.MapSQLiteError(err)
		}
		if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, domain.MapSQLiteError(err))
		}
		nextVersion := version
		if reverse {
			nextVersion = version - 1
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", nextVersion)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, domain.MapSQLiteError(err))
		}
		if err := tx.Commit(); err != nil {
			return domain.MapSQLiteError(err)
		}
		currentVersion = nextVersion
	}
	return nil
}

func (database *DB) SQL() *sql.DB { return database.sql }

func (database *DB) Close() error { return database.sql.Close() }
