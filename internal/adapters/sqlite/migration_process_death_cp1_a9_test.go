package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMigrationCP1A9CrashAfterDirtyAuthorityHelper(t *testing.T) {
	if os.Getenv("TALARIA_TEST_MIGRATION_CRASH_HELPER") != "1" {
		return
	}
	database, err := sql.Open("sqlite", "file:"+os.Getenv("TALARIA_TEST_MIGRATION_CRASH_PATH"))
	if err != nil {
		os.Exit(80)
	}
	if err := applyMigrationsWithTestOptions(context.Background(), database, testMigrationOptions{
		failpoints: testMigrationFailpoints{
			KillAfterDirtySetVersion: func(version uint) error {
				if os.Getenv("TALARIA_TEST_MIGRATION_KILL_AFTER_DIRTY_SET") == fmt.Sprintf("%d", version) {
					os.Exit(92)
				}
				return nil
			},
		},
	}); err != nil {
		os.Exit(81)
	}
	os.Exit(82)
}

func TestMigrationDirtyRecoveryAfterSetVersionBeforeRun(t *testing.T) {
	for _, test := range []struct {
		name   string
		seed   uint
		target string
	}{
		{name: "migration-2", seed: 1, target: "2"},
		{name: "migration-3", seed: 2, target: "3"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "set-version-before-run.db")
			seedMigrationDatabase(t, path, test.seed)
			command := exec.Command(os.Args[0], "-test.run=^TestMigrationCP1A9CrashAfterDirtyAuthorityHelper$", "-test.count=1")
			command.Env = append(os.Environ(),
				"TALARIA_TEST_MIGRATION_CRASH_HELPER=1",
				"TALARIA_TEST_MIGRATION_CRASH_PATH="+path,
				"TALARIA_TEST_MIGRATION_KILL_AFTER_DIRTY_SET="+test.target,
			)
			err := command.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 92 {
				t.Fatalf("crash helper error = %v, want exit 92", err)
			}

			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			var version int
			var dirty bool
			if err := database.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
				t.Fatal(err)
			}
			if version != int(test.target[0]-'0') || !dirty {
				t.Fatalf("authority after SetVersion crash = version %d dirty %v", version, dirty)
			}
			evidence, err := latestMigrationEvidence(context.Background(), database)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.FailureStage != migrationStageStarted || !evidence.Dirty || evidence.RunID == "" {
				t.Fatalf("SetVersion crash evidence = %+v, want durable started evidence", evidence)
			}
			if err := validateOrdinaryOpenSchema(context.Background(), database); err == nil {
				t.Fatal("ordinary open accepted dirty SetVersion crash")
			}
			if err := applyMigrations(context.Background(), database); err != nil {
				t.Fatalf("maintenance recovery after SetVersion crash: %v", err)
			}
			if err := validateOrdinaryOpenSchema(context.Background(), database); err != nil {
				t.Fatalf("recovered SetVersion crash is not ordinary-open ready: %v", err)
			}
		})
	}
}
