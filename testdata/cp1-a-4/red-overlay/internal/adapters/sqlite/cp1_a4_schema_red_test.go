package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

// T3 schema RED binds the epoch audit and migration run identity to the
// embedded SQLite schema rather than to a receipt-only assertion.
func TestSchemaCP1A4DurableActivationAudit(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/schema.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"activation_epoch_audit", "migration_journal"} {
		var count int
		if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("required durable table %q is absent", table)
		}
	}
	var runID, fingerprint string
	if err := database.QueryRow("SELECT run_id, schema_fingerprint FROM migration_journal ORDER BY id DESC LIMIT 1").Scan(&runID, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if runID == "" || fingerprint == "" {
		t.Fatalf("migration evidence is not bound to a run and schema: run_id=%q fingerprint=%q", runID, fingerprint)
	}
}
