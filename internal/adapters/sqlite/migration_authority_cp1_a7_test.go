package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestMigrationAuthorityCP1A7RejectsZeroRowsWithoutMutation(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/zero-rows.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(testCanonicalSchemaMigrationsDDL); err != nil {
		t.Fatal(err)
	}
	before := migrationAuthoritySchemaSnapshot(t, database)
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("zero-row migration authority was accepted")
	}
	if after := migrationAuthoritySchemaSnapshot(t, database); before != after {
		t.Fatalf("zero-row rejection mutated database: before=%s after=%s", before, after)
	}
}

func TestMigrationAuthorityCP1A7RejectsDuplicateRowsWithoutMutation(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/duplicate-rows.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE schema_migrations (version INTEGER, dirty INTEGER);
		INSERT INTO schema_migrations(version, dirty) VALUES (3, 0), (3, 0);
		PRAGMA user_version = 3;
	`); err != nil {
		t.Fatal(err)
	}
	before := migrationAuthoritySchemaSnapshot(t, database)
	if _, _, _, _, err := validateMigrationAuthority(context.Background(), database); err == nil {
		t.Fatal("duplicate migration authority rows were accepted")
	}
	if after := migrationAuthoritySchemaSnapshot(t, database); before != after {
		t.Fatalf("duplicate-row rejection mutated database: before=%s after=%s", before, after)
	}
}

func TestMigrationAuthorityCP1A7RejectsNullDirtyAndNonCanonicalValuesWithoutMutation(t *testing.T) {
	for _, fixture := range []struct {
		name, version, dirty string
	}{
		{name: "null-dirty", version: "3", dirty: "NULL"},
		{name: "text-version", version: "'3'", dirty: "0"},
		{name: "text-dirty", version: "3", dirty: "'false'"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/invalid-authority.db")
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			statement := `
				CREATE TABLE schema_migrations (version INTEGER, dirty INTEGER);
				CREATE UNIQUE INDEX version_unique ON schema_migrations(version);
				CREATE TABLE migration_journal (id INTEGER);
				INSERT INTO schema_migrations(version, dirty) VALUES (` + fixture.version + `, ` + fixture.dirty + `);
				PRAGMA user_version = 3;
			`
			if _, err := database.Exec(statement); err != nil {
				t.Fatal(err)
			}
			before := migrationAuthoritySchemaSnapshot(t, database)
			if _, _, _, _, err := validateMigrationAuthority(context.Background(), database); err == nil {
				t.Fatal("invalid migration authority was accepted")
			}
			if after := migrationAuthoritySchemaSnapshot(t, database); before != after {
				t.Fatalf("invalid-authority rejection mutated database: before=%s after=%s", before, after)
			}
		})
	}
}

func TestMigrationAuthorityCP1A7AllowsOnlyExactPreDDLJournallessMigrationOne(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/pre-ddl.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := ensureCanonicalMigrationTable(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		UPDATE schema_migrations SET version = 1, dirty = 1;
		PRAGMA user_version = 0;
	`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("exact pre-DDL migration-1 recovery failed: %v", err)
	}
	var version int
	var dirty bool
	if err := database.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != int(embeddedMigrationTarget) || dirty {
		t.Fatalf("recovered migration authority = version %d dirty %v", version, dirty)
	}
}

func TestMigrationAuthorityCP1A7RejectsPostDDLJournallessMigrationOneWithoutMutation(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/post-ddl.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(testCanonicalSchemaMigrationsDDL + `
		INSERT INTO schema_migrations(version, dirty) VALUES (1, 1);
		CREATE TABLE partial_migration_object (id INTEGER);
		PRAGMA user_version = 0;
	`); err != nil {
		t.Fatal(err)
	}
	before := migrationAuthoritySchemaSnapshot(t, database)
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("post-DDL journalless migration-1 recovery was accepted")
	}
	if after := migrationAuthoritySchemaSnapshot(t, database); before != after {
		t.Fatalf("post-DDL rejection mutated database: before=%s after=%s", before, after)
	}
}

func TestMigrationAuthorityCP1A7RequiresFinalJournalEvidence(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/final-journal.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		DELETE FROM migration_journal WHERE id = (SELECT max(id) FROM migration_journal);
		CREATE TRIGGER reject_final_migration_journal
		BEFORE INSERT ON migration_journal
		WHEN NEW.dirty = 0
		BEGIN SELECT RAISE(ABORT, 'injected final journal completion failure'); END;
	`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("final journal failure was swallowed")
	}
	if err := validateOrdinaryOpenSchema(context.Background(), database); err == nil {
		t.Fatal("ordinary open accepted clean authorities without final journal evidence")
	}
	if _, err := database.Exec("DROP TRIGGER reject_final_migration_journal"); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("maintenance did not complete final journal retry: %v", err)
	}
	if err := validateOrdinaryOpenSchema(context.Background(), database); err != nil {
		t.Fatalf("ordinary open rejected completed final journal retry: %v", err)
	}
}

func migrationAuthoritySchemaSnapshot(t *testing.T, database *sql.DB) string {
	t.Helper()
	var userVersion int
	if err := database.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(`
		SELECT type, name, COALESCE(sql, '')
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var objects []string
	for rows.Next() {
		var typ, name, definition string
		if err := rows.Scan(&typ, &name, &definition); err != nil {
			t.Fatal(err)
		}
		objects = append(objects, typ+":"+name+":"+definition)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	collectRows := func(query string) []string {
		rows, err := database.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var values []string
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return values
	}
	var authorityRows []string
	if present, err := migrationTableExists(context.Background(), database); err != nil {
		t.Fatal(err)
	} else if present {
		authorityRows = collectRows("SELECT quote(rowid)||':'||quote(version)||':'||quote(dirty) FROM schema_migrations ORDER BY rowid")
	}
	var journalRows []string
	if present, err := migrationJournalExists(context.Background(), database); err != nil {
		t.Fatal(err)
	} else if present {
		columns := collectRows("SELECT name FROM pragma_table_info('migration_journal') ORDER BY cid")
		canonicalColumns := []string{"id", "run_id", "target_version", "current_version", "failed_version", "completed_version", "backup_id", "failure_stage", "schema_fingerprint", "dirty", "safe_error", "started_at", "updated_at"}
		if strings.Join(columns, "|") == strings.Join(canonicalColumns, "|") {
			journalRows = collectRows(`
					SELECT quote(id)||':'||quote(run_id)||':'||quote(target_version)||':'||quote(current_version)||':'||
					       quote(failed_version)||':'||quote(completed_version)||':'||quote(backup_id)||':'||
					       quote(failure_stage)||':'||quote(schema_fingerprint)||':'||quote(dirty)||':'||
					       quote(safe_error)||':'||quote(started_at)||':'||quote(updated_at)
					FROM migration_journal ORDER BY id`)
		}
	}
	var sequenceRows []string
	var sequencePresent int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'sqlite_sequence'").Scan(&sequencePresent); err != nil {
		t.Fatal(err)
	}
	if sequencePresent == 1 {
		sequenceRows = collectRows(`
			SELECT quote(name)||':'||quote(seq)
			FROM sqlite_sequence
			WHERE name IN ('schema_migrations', 'migration_journal')
			ORDER BY name`)
	}
	return fmt.Sprintf("user=%d schema_migrations=%s migration_journal=%s sqlite_sequence=%s objects=%s",
		userVersion, strings.Join(authorityRows, "|"), strings.Join(journalRows, "|"), strings.Join(sequenceRows, "|"), strings.Join(objects, "|"))
}
