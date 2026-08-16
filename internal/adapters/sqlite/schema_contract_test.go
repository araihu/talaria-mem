package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func TestSchemaContractFingerprint(t *testing.T) {
	database := openTestDB(t)
	got, err := schemaContractFingerprint(t.Context(), database.SQL())
	if err != nil {
		t.Fatal(err)
	}
	if canonicalSchemaContractFingerprint == "" {
		t.Logf("canonical schema contract fingerprint=%s", got)
		return
	}
	if got != canonicalSchemaContractFingerprint {
		t.Fatalf("schema contract fingerprint=%s, want %s", got, canonicalSchemaContractFingerprint)
	}
}

func TestSchemaCP1A5OrdinaryContractRejectsTamperedObject(t *testing.T) {
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/tampered.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DROP TABLE outbox"); err != nil {
		t.Fatal(err)
	}
	if err := validateOrdinaryOpenSchema(context.Background(), database); err == nil {
		t.Fatal("ordinary schema validation accepted a missing non-FTS object")
	}
}
