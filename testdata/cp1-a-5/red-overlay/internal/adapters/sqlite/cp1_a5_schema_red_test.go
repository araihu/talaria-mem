package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

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
