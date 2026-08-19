package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/db"
)

const testCanonicalSchemaMigrationsDDL = `CREATE TABLE schema_migrations (version INTEGER NOT NULL CHECK (version >= 0), dirty INTEGER NOT NULL CHECK (dirty IN (0,1))); CREATE UNIQUE INDEX version_unique ON schema_migrations(version);`

func TestOrdinaryOpenRejectsUnsupportedCleanAndDirtyVersions(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "dirty"}[dirty], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsupported.db")
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			authorityUserVersion := 4
			if dirty {
				authorityUserVersion = 3
			}
			if _, err := database.Exec(testCanonicalSchemaMigrationsDDL+` INSERT INTO schema_migrations(version, dirty) VALUES (4, ?); PRAGMA user_version = `+fmt.Sprintf("%d", authorityUserVersion), dirty); err != nil {
				database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if opened, err := OpenWithManagedPathPolicy(context.Background(), path, testManagedPathPolicy{}); err == nil {
				opened.Close()
				t.Fatal("ordinary open accepted unsupported schema version")
			}
			check, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer check.Close()
			var userVersion int
			if err := check.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
				t.Fatal(err)
			}
			if userVersion != authorityUserVersion {
				t.Fatalf("unsupported ordinary open mutated user_version = %d, want %d", userVersion, authorityUserVersion)
			}
		})
	}
}

func TestOrdinaryOpenRejectsCleanOlderVersions(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "older.db")
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(testCanonicalSchemaMigrationsDDL+` CREATE TABLE migration_journal (id INTEGER); INSERT INTO schema_migrations(version, dirty) VALUES (?, 0); PRAGMA user_version = `+fmt.Sprintf("%d", version), version); err != nil {
				database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if opened, err := Open(context.Background(), path, testManagedPathPolicy{}); err == nil {
				opened.Close()
				t.Fatalf("ordinary open accepted clean schema version %d", version)
			}
		})
	}
}

func seedMigrationDatabase(t *testing.T, path string, version uint) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, name := range []struct {
		number uint
		name   string
	}{{1, "000001_core.up.sql"}, {2, "000002_usage_security.up.sql"}} {
		if name.number > version {
			continue
		}
		contents, err := db.Migrations.ReadFile("migrations/" + name.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(string(contents)); err != nil {
			t.Fatalf("seed %s: %v", name.name, err)
		}
	}
	if err := ensureCanonicalMigrationTable(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE schema_migrations SET version = ?, dirty = 0`, version); err != nil {
		t.Fatal(err)
	}
	if err := setSQLiteUserVersion(context.Background(), database, version); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := migrationSchemaFingerprint(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.Exec(`
		INSERT INTO migration_journal(
			run_id, target_version, current_version, completed_version,
			failure_stage, schema_fingerprint, dirty, safe_error, started_at, updated_at
		) VALUES ('seed-migration', ?, ?, ?, '', ?, 0, '', ?, ?)`,
		embeddedMigrationTarget, version, version, fingerprint, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationDirtyRecoveryFailsClosedForUnknownStage(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/unknown.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE schema_migrations SET version = 3, dirty = 1"); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := migrationSchemaFingerprint(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.Exec("INSERT INTO migration_journal(run_id, target_version, current_version, failed_version, completed_version, failure_stage, schema_fingerprint, dirty, safe_error, started_at, updated_at) VALUES ('migration-unknown', 3, 3, 3, 2, 'started', ?, 1, 'unknown', ?, ?)", fingerprint, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("unknown dirty recovery stage was accepted")
	}
}

func TestMigrationDirtyRecoveryFailsClosedAboveTarget(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/above-target.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE schema_migrations SET version = 4, dirty = 1"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("above-target dirty migration was accepted")
	}
}

func TestMigrationRoundTrip(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/roundtrip.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version int
	var dirty bool
	if err := database.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != int(embeddedMigrationTarget) || dirty {
		t.Fatalf("migration protocol state = version %d dirty %v", version, dirty)
	}
	var completed, failed sql.NullInt64
	if err := database.QueryRow("SELECT completed_version, failed_version FROM migration_journal ORDER BY id DESC LIMIT 1").Scan(&completed, &failed); err != nil {
		t.Fatal(err)
	}
	if !completed.Valid || completed.Int64 != int64(embeddedMigrationTarget) || failed.Valid {
		t.Fatalf("migration journal completion = completed %v failed %v", completed, failed)
	}
	if err := applyDownMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("migration round trip left %d user tables", count)
	}
}

func TestMigrationRoundTripMigrationJournalOrdering(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/ordering.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query("PRAGMA foreign_key_list(migration_journal)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration journal has an early managed_backups foreign key")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRejectsNonTransactionalMaintenance(t *testing.T) {
	for _, test := range []struct {
		name string
		sql  string
	}{
		{name: "vacuum", sql: "VACUUM"},
		{name: "journal mode", sql: "PRAGMA journal_mode = WAL"},
		{name: "wal checkpoint", sql: "PRAGMA wal_checkpoint(TRUNCATE)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMigrationSQL("fixture.up.sql", []byte(test.sql)); err == nil {
				t.Fatal("non-transactional maintenance accepted")
			}
		})
	}
}

func TestMigrationResumesVerifiedDirtyVersion(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/resume.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"curation_jobs", "curation_session_counters"} {
		if _, err := database.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec("UPDATE schema_migrations SET version = 4, dirty = 1"); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := migrationSchemaFingerprint(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.Exec("INSERT INTO migration_journal(run_id, target_version, current_version, failed_version, completed_version, failure_stage, schema_fingerprint, dirty, safe_error, started_at, updated_at) VALUES ('migration-fixture', 4, 4, 4, 3, 'rollback_before_commit', ?, 1, 'fixture', ?, ?)", fingerprint, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("dirty migration did not resume: %v", err)
	}
	var dirty bool
	if err := database.QueryRow("SELECT dirty FROM schema_migrations").Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("migration remained dirty after resume")
	}
}

func TestMigrationRejectsPreJournalVersionWithoutJournal(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/legacy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, name := range []string{"000001_core.up.sql", "000002_usage_security.up.sql"} {
		contents, err := db.Migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(string(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec("DROP TABLE migration_journal; PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("pre-journal migration without journal was accepted")
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_journal'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("pre-journal rejection recreated migration journal")
	}
}
