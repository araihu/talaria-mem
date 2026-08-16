package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func TestSchemaMigrationJournalFailureStage(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/schema.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var stage string
	if err := database.QueryRow("SELECT failure_stage FROM migration_journal LIMIT 1").Scan(&stage); err != nil {
		t.Fatalf("migration journal failure-stage schema missing: %v", err)
	}
}
