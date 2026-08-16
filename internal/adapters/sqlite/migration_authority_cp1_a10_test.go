package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestMigrationCP1A10RejectsMalformedJournalBeforeDirtyRecovery(t *testing.T) {
	for _, test := range []struct {
		name   string
		seed   uint
		target uint
	}{
		{name: "migration-2", seed: 1, target: 2},
		{name: "migration-3", seed: 2, target: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir() + "/malformed-journal.db"
			seedMigrationDatabase(t, path, test.seed)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()

			fingerprint, err := migrationSchemaFingerprint(context.Background(), database)
			if err != nil {
				t.Fatal(err)
			}
			if err := replaceMigrationJournalWithExtraColumn(database, fingerprint, test.target); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(
				"UPDATE schema_migrations SET version = ?, dirty = 1",
				test.target,
			); err != nil {
				t.Fatal(err)
			}
			if err := setSQLiteUserVersion(context.Background(), database, test.seed); err != nil {
				t.Fatal(err)
			}
			before := migrationAuthoritySchemaSnapshot(t, database)
			if err := applyMigrations(context.Background(), database); err == nil {
				t.Fatal("dirty recovery accepted an extra-column migration journal")
			}
			if after := migrationAuthoritySchemaSnapshot(t, database); before != after {
				t.Fatalf("malformed-journal rejection mutated schema or authority: before=%s after=%s", before, after)
			}
		})
	}
}

func replaceMigrationJournalWithExtraColumn(database *sql.DB, fingerprint string, target uint) error {
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.Exec(`
		ALTER TABLE migration_journal RENAME TO migration_journal_original;
		CREATE TABLE migration_journal (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL DEFAULT '',
			target_version INTEGER NOT NULL CHECK (target_version >= 0),
			current_version INTEGER NOT NULL CHECK (current_version >= 0),
			failed_version INTEGER CHECK (failed_version IS NULL OR failed_version >= 0),
			completed_version INTEGER CHECK (completed_version IS NULL OR completed_version >= 0),
			backup_id TEXT,
			failure_stage TEXT NOT NULL DEFAULT '' CHECK (failure_stage IN ('', 'started', 'rollback_before_commit', 'applied_ddl_before_clean')),
			schema_fingerprint TEXT NOT NULL DEFAULT '',
			dirty INTEGER NOT NULL DEFAULT 0 CHECK (dirty IN (0, 1)),
			safe_error TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			rogue TEXT,
			CHECK (length(run_id) <= 128 AND instr(run_id, char(0)) = 0 AND instr(run_id, char(10)) = 0 AND instr(run_id, char(13)) = 0),
			CHECK (length(schema_fingerprint) <= 64 AND instr(schema_fingerprint, char(0)) = 0 AND instr(schema_fingerprint, char(10)) = 0 AND instr(schema_fingerprint, char(13)) = 0),
			CHECK (length(safe_error) <= 512 AND instr(safe_error, char(0)) = 0 AND instr(safe_error, char(10)) = 0 AND instr(safe_error, char(13)) = 0)
		) STRICT;
		INSERT INTO migration_journal(
			run_id, target_version, current_version, failed_version, completed_version,
			failure_stage, schema_fingerprint, dirty, safe_error, started_at, updated_at
		) VALUES ('migration-cp1-a10', ?, ?, ?, ?, 'started', ?, 1, '', ?, ?);
		DROP TABLE migration_journal_original;
	`, embeddedMigrationTarget, target, target, target-1, fingerprint, timestamp, timestamp); err != nil {
		return fmt.Errorf("replace migration journal: %w", err)
	}
	return nil
}
