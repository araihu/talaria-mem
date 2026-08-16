package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const cp1A7RejectedProtocolDDL = `
CREATE TABLE schema_migrations (version uint64, dirty bool);
CREATE UNIQUE INDEX version_unique ON schema_migrations (version);
`

func TestMigrationAuthorityCP1A7RejectsZeroRowsWithoutMutation(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/zero-rows.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(cp1A7RejectedProtocolDDL); err != nil {
		t.Fatal(err)
	}
	before := cp1A7Snapshot(t, database)
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("zero-row migration authority was accepted")
	}
	if after := cp1A7Snapshot(t, database); before != after {
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
		CREATE TABLE schema_migrations (version uint64, dirty bool);
		INSERT INTO schema_migrations(version, dirty) VALUES (3, 0), (3, 0);
		PRAGMA user_version = 3;
	`); err != nil {
		t.Fatal(err)
	}
	before := cp1A7Snapshot(t, database)
	if _, _, _, _, err := validateMigrationAuthority(context.Background(), database); err == nil {
		t.Fatal("duplicate migration authority rows were accepted")
	}
	if after := cp1A7Snapshot(t, database); before != after {
		t.Fatalf("duplicate-row rejection mutated database: before=%s after=%s", before, after)
	}
}

func TestMigrationAuthorityCP1A7RejectsNullDirtyWithoutMutation(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/null-dirty.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(cp1A7RejectedProtocolDDL + `
CREATE TABLE migration_journal (id INTEGER);
INSERT INTO schema_migrations(version, dirty) VALUES (3, NULL);
PRAGMA user_version = 3;
`); err != nil {
		t.Fatal(err)
	}
	before := cp1A7Snapshot(t, database)
	if _, _, _, _, err := validateMigrationAuthority(context.Background(), database); err == nil {
		t.Fatal("NULL dirty authority was accepted")
	}
	if after := cp1A7Snapshot(t, database); before != after {
		t.Fatalf("NULL dirty rejection mutated database: before=%s after=%s", before, after)
	}
}

func TestMigrationAuthorityCP1A7RejectsNonCanonicalAuthorityValuesWithoutMutation(t *testing.T) {
	for _, fixture := range []struct {
		name, version, dirty string
	}{
		{name: "text-version", version: "'3'", dirty: "0"},
		{name: "text-dirty", version: "3", dirty: "'false'"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/noncanonical.db")
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			statement := cp1A7RejectedProtocolDDL + fmt.Sprintf(`
CREATE TABLE migration_journal (id INTEGER);
INSERT INTO schema_migrations(version, dirty) VALUES (%s, %s);
PRAGMA user_version = 3;
`, fixture.version, fixture.dirty)
			if _, err := database.Exec(statement); err != nil {
				t.Fatal(err)
			}
			before := cp1A7Snapshot(t, database)
			if _, _, _, _, err := validateMigrationAuthority(context.Background(), database); err == nil {
				t.Fatal("non-canonical migration authority was accepted")
			}
			if after := cp1A7Snapshot(t, database); before != after {
				t.Fatalf("non-canonical rejection mutated database: before=%s after=%s", before, after)
			}
		})
	}
}

func TestMigrationAuthorityCP1A7RejectsPostDDLJournallessVersionWithoutMutation(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/post-ddl.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(cp1A7RejectedProtocolDDL + `
INSERT INTO schema_migrations(version, dirty) VALUES (1, 1);
CREATE TABLE partial_migration_object (id INTEGER);
PRAGMA user_version = 0;
`); err != nil {
		t.Fatal(err)
	}
	before := cp1A7Snapshot(t, database)
	if _, _, _, _, err := validateMigrationAuthority(context.Background(), database); err == nil {
		t.Fatal("post-DDL journalless migration-1 authority was accepted")
	}
	if after := cp1A7Snapshot(t, database); before != after {
		t.Fatalf("post-DDL journalless rejection mutated database: before=%s after=%s", before, after)
	}
}

func TestMigrationAuthorityCP1A7RejectsCleanSchemaWithoutFinalJournalEvidence(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/final-journal.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	failpoints := migrationFailpoints{
		JournalWrite: func(version uint) error {
			if version != embeddedMigrationTarget {
				return nil
			}
			var dirty sql.NullBool
			if err := database.QueryRow("SELECT dirty FROM schema_migrations").Scan(&dirty); err != nil {
				return err
			}
			if dirty.Valid && !dirty.Bool {
				return errors.New("injected final journal completion failure")
			}
			return nil
		},
	}
	if err := applyMigrationsWithOptions(context.Background(), database, migrationOptions{failpoints: failpoints}); err == nil {
		t.Fatal("final journal failure was swallowed")
	}
	if err := validateOrdinaryOpenSchema(context.Background(), database); err == nil {
		t.Fatal("ordinary open accepted clean authorities without final journal evidence")
	}
}

func cp1A7Snapshot(t *testing.T, database *sql.DB) string {
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
	var authority string
	var version, dirty string
	if err := database.QueryRow("SELECT quote(version), quote(dirty) FROM schema_migrations LIMIT 1").Scan(&version, &dirty); err == nil {
		authority = fmt.Sprint(version, "/", dirty)
	}
	return fmt.Sprintf("user=%d authority=%s objects=%s", userVersion, authority, strings.Join(objects, "|"))
}
