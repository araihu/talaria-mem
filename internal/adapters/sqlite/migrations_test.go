package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

func TestMigrationRoundTrip(t *testing.T) {
	var fixture struct {
		TargetVersion    int    `json:"target_version"`
		CommittedVersion int    `json:"committed_version"`
		Expected         string `json:"expected"`
	}
	testutil.ReadJSONFixture(t, &fixture, "sqlite", "partial-migration-v4.json")
	if fixture.TargetVersion != 4 || fixture.CommittedVersion != 3 || fixture.Expected == "" {
		t.Fatalf("unexpected migration fixture: %+v", fixture)
	}
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/roundtrip.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := ApplyMigrations(context.Background(), database); err != nil {
		// RED expects the missing implementation to fail the literal command.
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("durable schema_migrations authority count = %d, want 1", count)
	}
}
