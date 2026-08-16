package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestMigrationCP1A11RejectsAncillaryAuthorityObjectsWithoutMutation(t *testing.T) {
	states := []struct {
		name   string
		seed   uint
		target uint
		dirty  bool
	}{
		{name: "clean-current", seed: embeddedMigrationTarget, target: embeddedMigrationTarget},
		{name: "dirty-v2", seed: 1, target: 2, dirty: true},
		{name: "dirty-v3", seed: 2, target: 3, dirty: true},
	}
	objects := []struct {
		name  string
		setup func(*sql.DB) error
	}{
		{
			name: "journal-trigger",
			setup: func(database *sql.DB) error {
				_, err := database.Exec(`
					CREATE TRIGGER cp1_a11_journal_corruptor
					AFTER INSERT ON migration_journal
					BEGIN
						UPDATE schema_migrations SET dirty = 1;
					END`)
				return err
			},
		},
		{
			name: "authority-trigger",
			setup: func(database *sql.DB) error {
				_, err := database.Exec(`
					CREATE TRIGGER cp1_a11_authority_corruptor
					AFTER UPDATE ON schema_migrations
					BEGIN
						UPDATE schema_migrations SET dirty = 1;
					END`)
				return err
			},
		},
		{
			name: "journal-index",
			setup: func(database *sql.DB) error {
				_, err := database.Exec("CREATE INDEX cp1_a11_journal_index ON migration_journal(run_id)")
				return err
			},
		},
		{
			name: "authority-index",
			setup: func(database *sql.DB) error {
				_, err := database.Exec("CREATE INDEX cp1_a11_authority_index ON schema_migrations(dirty)")
				return err
			},
		},
		{
			name: "journal-view",
			setup: func(database *sql.DB) error {
				_, err := database.Exec("CREATE VIEW cp1_a11_journal_view AS SELECT id FROM migration_journal")
				return err
			},
		},
		{
			name: "authority-view",
			setup: func(database *sql.DB) error {
				_, err := database.Exec("CREATE VIEW cp1_a11_authority_view AS SELECT version FROM schema_migrations")
				return err
			},
		},
	}
	for _, state := range states {
		for _, object := range objects {
			t.Run(state.name+"/"+object.name, func(t *testing.T) {
				path := t.TempDir() + "/ancillary-authority.db"
				seedMigrationDatabase(t, path, state.seed)
				database, err := sql.Open("sqlite", "file:"+path)
				if err != nil {
					t.Fatal(err)
				}
				defer database.Close()
				if err := object.setup(database); err != nil {
					t.Fatal(err)
				}
				if state.dirty {
					prepareDirtyMigrationCP1A11(t, database, state.target)
				}
				before := migrationAuthoritySchemaSnapshot(t, database)
				if err := applyMigrations(context.Background(), database); err == nil {
					t.Fatal("ancillary authority object was accepted")
				}
				if after := migrationAuthoritySchemaSnapshot(t, database); before != after {
					t.Fatalf("ancillary authority rejection mutated state: before=%s after=%s", before, after)
				}
			})
		}
	}
}

func TestMigrationCP1A11RejectsInvalidOrReversedJournalTimestampsWithoutMutation(t *testing.T) {
	states := []struct {
		name   string
		seed   uint
		target uint
		dirty  bool
	}{
		{name: "clean-current", seed: embeddedMigrationTarget, target: embeddedMigrationTarget},
		{name: "dirty-v2", seed: 1, target: 2, dirty: true},
		{name: "dirty-v3", seed: 2, target: 3, dirty: true},
	}
	timestamps := []struct {
		name    string
		started string
		updated string
	}{
		{name: "malformed", started: "not-a-time", updated: "not-a-time"},
		{name: "reversed", started: "2026-08-15T00:00:01Z", updated: "2026-08-15T00:00:00Z"},
	}
	for _, state := range states {
		for _, timestamp := range timestamps {
			t.Run(state.name+"/"+timestamp.name, func(t *testing.T) {
				path := t.TempDir() + "/timestamp-authority.db"
				seedMigrationDatabase(t, path, state.seed)
				database, err := sql.Open("sqlite", "file:"+path)
				if err != nil {
					t.Fatal(err)
				}
				defer database.Close()
				if state.dirty {
					prepareDirtyMigrationCP1A11(t, database, state.target)
				}
				if _, err := database.Exec(
					"UPDATE migration_journal SET started_at = ?, updated_at = ? WHERE id = (SELECT max(id) FROM migration_journal)",
					timestamp.started, timestamp.updated,
				); err != nil {
					t.Fatal(err)
				}
				before := migrationAuthoritySchemaSnapshot(t, database)
				if err := applyMigrations(context.Background(), database); err == nil {
					t.Fatal("invalid migration journal timestamps were accepted")
				}
				if after := migrationAuthoritySchemaSnapshot(t, database); before != after {
					t.Fatalf("timestamp rejection mutated state: before=%s after=%s", before, after)
				}
			})
		}
	}
}

func prepareDirtyMigrationCP1A11(t *testing.T, database *sql.DB, target uint) {
	t.Helper()
	fingerprint, err := migrationSchemaFingerprint(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.Exec("UPDATE schema_migrations SET version = ?, dirty = 1", target); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		UPDATE migration_journal SET
			target_version = ?, current_version = ?, failed_version = ?, completed_version = ?,
			failure_stage = 'started', schema_fingerprint = ?, dirty = 1, safe_error = '',
			started_at = ?, updated_at = ?
			WHERE id = (SELECT max(id) FROM migration_journal);`,
		embeddedMigrationTarget, target, target, target-1, fingerprint, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if err := setSQLiteUserVersion(context.Background(), database, target-1); err != nil {
		t.Fatal(err)
	}
}
