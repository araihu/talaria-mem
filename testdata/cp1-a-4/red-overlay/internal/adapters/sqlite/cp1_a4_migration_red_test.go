package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

// T3 migration RED is intentionally named under TestMigrationRoundTrip so
// the exact planned migration gate exercises the missing durability contract.
func TestMigrationRoundTripCP1A4DurableEvidence(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/roundtrip.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'activation_epoch_audit'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("round-trip schema omitted activation epoch audit: count=%d", count)
	}
}
