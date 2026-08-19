package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestMigrationAuthorityCP1A6RejectsDivergentCleanAuthoritiesWithoutMutation(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/authority.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("maintenance accepted divergent clean migration authorities")
	}
	var userVersion int
	if err := database.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatal(err)
	}
	if userVersion != 3 {
		t.Fatalf("rejected authority mutated user_version to %d", userVersion)
	}
}

func TestMigrationAuthorityCP1A6RejectsDuplicateRows(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/duplicate.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE schema_migrations (version uint64, dirty bool);
		INSERT INTO schema_migrations(version, dirty) VALUES (3, 0), (3, 0);
	`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := migrationVersion(context.Background(), database); err == nil {
		t.Fatal("duplicate migration authority rows were accepted")
	}
}

func TestMigrationAuthorityCP1A6RejectsAlteredProtocolDDL(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/protocol.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		DROP INDEX version_unique;
		ALTER TABLE schema_migrations RENAME TO schema_migrations_original;
		CREATE TABLE schema_migrations (version TEXT, dirty INTEGER);
		CREATE UNIQUE INDEX version_unique ON schema_migrations (version);
		INSERT INTO schema_migrations(version, dirty) VALUES ('3', 0);
		DROP TABLE schema_migrations_original;
	`); err != nil {
		t.Fatal(err)
	}
	if err := validateOrdinaryOpenSchema(context.Background(), database); err == nil {
		t.Fatal("ordinary open accepted altered migration protocol DDL")
	}
}

func TestMigrationAuthorityCP1A6RejectsExistingVersionWithoutJournal(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/journal.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DROP TABLE migration_journal"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("existing migration version without journal was accepted")
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_journal'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected missing journal was recreated: count=%d", count)
	}
}

func TestMigrationAuthorityCP1A6RejectsAboveTargetWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		version int
		dirty   int
		user    int
	}{
		{name: "clean-schema-authority", version: 4, dirty: 0, user: 4},
		{name: "dirty-schema-authority", version: 4, dirty: 1, user: 3},
		{name: "clean-user-authority", version: 3, dirty: 0, user: 4},
		{name: "dirty-user-authority", version: 3, dirty: 1, user: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/above-target.db")
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if _, err := database.Exec(testCanonicalSchemaMigrationsDDL+` INSERT INTO schema_migrations(version, dirty) VALUES (?, ?);`, test.version, test.dirty); err != nil {
				t.Fatal(err)
			}
			if err := setSQLiteUserVersion(context.Background(), database, uint(test.user)); err != nil {
				t.Fatal(err)
			}
			before, err := authoritySnapshot(t, database)
			if err != nil {
				t.Fatal(err)
			}
			if err := applyMigrations(context.Background(), database); err == nil {
				t.Fatal("above-target migration authority was accepted")
			}
			after, err := authoritySnapshot(t, database)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("rejected above-target authority mutated state: before=%s after=%s", before, after)
			}
		})
	}
}

func authoritySnapshot(t *testing.T, database *sql.DB) (string, error) {
	t.Helper()
	var userVersion int
	if err := database.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		return "", err
	}
	var version int
	var dirty bool
	if err := database.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d/%d/%t", userVersion, version, dirty), nil
}

func TestMigrationAuthorityCP1A6RejectsMissingJournalAtExistingVersions(t *testing.T) {
	for _, version := range []uint{1, 2, 3} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			path := t.TempDir() + "/missing-journal.db"
			seedMigrationDatabase(t, path, version)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if _, err := database.Exec("DROP TABLE migration_journal"); err != nil {
				t.Fatal(err)
			}
			before, err := authoritySnapshot(t, database)
			if err != nil {
				t.Fatal(err)
			}
			if err := applyMigrations(context.Background(), database); err == nil {
				t.Fatal("existing schema without migration journal was accepted")
			}
			after, err := authoritySnapshot(t, database)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("missing-journal rejection mutated authority: before=%s after=%s", before, after)
			}
			var count int
			if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_journal'").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("missing journal was recreated for version %d", version)
			}
		})
	}
}
