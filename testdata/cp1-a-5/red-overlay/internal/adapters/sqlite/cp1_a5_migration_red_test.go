package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestMigrationRoundTripCP1A5JournalWriteFailure(t *testing.T) {
	migrationJournalWriteHook = func(version uint) error {
		if version == 2 {
			return errors.New("injected journal write failure")
		}
		return nil
	}
	t.Cleanup(func() { migrationJournalWriteHook = nil })
	database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/journal-write-failure.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err == nil {
		t.Fatal("migration journal write failure was swallowed")
	}
}
