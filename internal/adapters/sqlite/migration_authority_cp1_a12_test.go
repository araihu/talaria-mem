package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestMigrationCP1A12ClampsJournalUpdatesToPersistedStartAcrossWindows(t *testing.T) {
	states := []struct {
		name   string
		seed   uint
		target uint
		dirty  bool
	}{
		{name: "dirty-v2", seed: 1, target: 2, dirty: true},
		{name: "dirty-v3", seed: 2, target: 3, dirty: true},
		{name: "clean-final", seed: embeddedMigrationTarget, target: embeddedMigrationTarget},
	}
	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			path := t.TempDir() + "/clock-authority.db"
			seedMigrationDatabase(t, path, state.seed)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			journalID, persistedStarted := prepareFutureStartedMigrationCP1A12(t, database, state.target, state.dirty)

			fingerprint, err := migrationSchemaFingerprint(context.Background(), database)
			if err != nil {
				t.Fatal(err)
			}
			completed := state.target
			var failed *uint
			failureStage := ""
			safeError := ""
			if state.dirty {
				failedVersion := state.target
				failed = &failedVersion
				completed--
				failureStage = migrationStageStarted
			}
			if err := updateMigrationStateWithStage(
				context.Background(), database, journalID, "cp1-a-12-"+state.name,
				state.target, failed, &completed, safeError, failureStage, state.dirty, fingerprint,
			); err != nil {
				t.Fatal(err)
			}
			var updated string
			if err := database.QueryRow("SELECT updated_at FROM migration_journal WHERE id = ?", journalID).Scan(&updated); err != nil {
				t.Fatal(err)
			}
			if updated != persistedStarted {
				t.Fatalf("backward clock was not clamped: updated_at=%q persisted started_at=%q", updated, persistedStarted)
			}
			if err := validateMigrationJournalSchema(context.Background(), database); err != nil {
				t.Fatalf("journal update was not clamped to persisted started_at: %v", err)
			}
		})
	}
}

func TestMigrationCP1A12ValidatesEveryJournalRowBeforeMaintenanceSuccess(t *testing.T) {
	states := []struct {
		name   string
		seed   uint
		target uint
		dirty  bool
	}{
		{name: "clean-final", seed: embeddedMigrationTarget, target: embeddedMigrationTarget},
		{name: "dirty-v2", seed: 1, target: 2, dirty: true},
		{name: "dirty-v3", seed: 2, target: 3, dirty: true},
	}
	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			path := t.TempDir() + "/final-validation.db"
			seedMigrationDatabase(t, path, state.seed)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			prepareFutureStartedMigrationCP1A12(t, database, state.target, state.dirty)

			err = applyMigrationsWithTestOptions(context.Background(), database, testMigrationOptions{
				failpoints: testMigrationFailpoints{
					BeforeFinalJournal: func(uint) error {
						_, updateErr := database.Exec(
							"UPDATE migration_journal SET updated_at = ? WHERE id = (SELECT max(id) FROM migration_journal)",
							"2000-01-01T00:00:00Z",
						)
						return updateErr
					},
				},
			})
			if err == nil {
				t.Fatal("maintenance returned success with an invalid historical journal row")
			}
		})
	}
}

func prepareFutureStartedMigrationCP1A12(t *testing.T, database *sql.DB, target uint, dirty bool) (int64, string) {
	t.Helper()
	if dirty {
		prepareDirtyMigrationCP1A11(t, database, target)
	}
	future := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := database.Exec(
		"UPDATE migration_journal SET started_at = ?, updated_at = ? WHERE id = (SELECT max(id) FROM migration_journal)",
		future, future,
	); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := database.QueryRow("SELECT max(id) FROM migration_journal").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id, future
}
