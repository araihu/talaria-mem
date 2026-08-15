package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func TestMigrationRoundTrip(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/roundtrip.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := ApplyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDownMigrations(context.Background(), database); err != nil {
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
