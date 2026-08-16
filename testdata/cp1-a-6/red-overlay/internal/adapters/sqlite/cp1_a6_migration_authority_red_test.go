package sqlite

import (
	"context"
	"database/sql"
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
	if _, err := database.Exec("PRAGMA user_version = 4"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("maintenance accepted divergent clean migration authorities")
	}
	var userVersion int
	if err := database.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatal(err)
	}
	if userVersion != 4 {
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
