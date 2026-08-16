package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

// This exact TestMigrationRoundTrip overlay checks the migration-1 ordering
// defect: the rejected ancestor declared a managed_backups FK before that
// table existed in migration 3.
func TestMigrationRoundTripMissingJournalOrdering(t *testing.T) {
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
