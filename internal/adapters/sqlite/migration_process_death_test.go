package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationTestFailpointCrashHelper(t *testing.T) {
	if os.Getenv("TALARIA_TEST_MIGRATION_CRASH_HELPER") != "1" {
		return
	}
	database, err := sql.Open("sqlite", "file:"+os.Getenv("TALARIA_TEST_MIGRATION_CRASH_PATH"))
	if err != nil {
		os.Exit(80)
	}
	if err := applyMigrationsWithTestOptions(context.Background(), database, testMigrationOptions{failpoints: testMigrationCrashFailpoints()}); err != nil {
		os.Exit(81)
	}
	os.Exit(82)
}

func testMigrationCrashFailpoints() testMigrationFailpoints {
	return testMigrationFailpoints{
		AfterCleanVersion: func(version uint) error {
			if os.Getenv("TALARIA_TEST_MIGRATION_KILL_AFTER_CLEAN") == fmt.Sprintf("%d", version) {
				os.Exit(95)
			}
			return nil
		},
		BeforeUserVersion: func(version uint) error {
			if os.Getenv("TALARIA_TEST_MIGRATION_KILL_BEFORE_USER_VERSION") == fmt.Sprintf("%d", version) {
				os.Exit(96)
			}
			return nil
		},
		AfterUserVersion: func(version uint) error {
			if os.Getenv("TALARIA_TEST_MIGRATION_KILL_AFTER_USER_VERSION") == fmt.Sprintf("%d", version) {
				os.Exit(97)
			}
			return nil
		},
		BeforeFinalJournal: func(version uint) error {
			if os.Getenv("TALARIA_TEST_MIGRATION_KILL_BEFORE_FINAL_JOURNAL") == fmt.Sprintf("%d", version) {
				os.Exit(98)
			}
			return nil
		},
		AfterFinalJournal: func(version uint) error {
			if os.Getenv("TALARIA_TEST_MIGRATION_KILL_AFTER_FINAL_JOURNAL") == fmt.Sprintf("%d", version) {
				os.Exit(99)
			}
			return nil
		},
		KillBeforeCommit: func(version uint) error {
			if os.Getenv("TALARIA_TEST_MIGRATION_KILL_BEFORE_COMMIT") == fmt.Sprintf("%d", version) {
				os.Exit(93)
			}
			return nil
		},
		KillAfterDDL: func(version uint) error {
			if os.Getenv("TALARIA_TEST_MIGRATION_KILL_AFTER_DDL") == fmt.Sprintf("%d", version) {
				os.Exit(94)
			}
			return nil
		},
	}
}

func TestMigrationCrashAfterCleanAndAuthorityWrites(t *testing.T) {
	for _, test := range []struct {
		name, variable string
		code           int
	}{
		{name: "after-clean-version-1", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_CLEAN=1", code: 95},
		{name: "after-clean-version-2", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_CLEAN=2", code: 95},
		{name: "after-clean-version-3", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_CLEAN=3", code: 95},
		{name: "after-clean-version-4", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_CLEAN=4", code: 95},
		{name: "before-user-version", variable: "TALARIA_TEST_MIGRATION_KILL_BEFORE_USER_VERSION=4", code: 96},
		{name: "after-user-version", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_USER_VERSION=4", code: 97},
		{name: "before-final-journal", variable: "TALARIA_TEST_MIGRATION_KILL_BEFORE_FINAL_JOURNAL=4", code: 98},
		{name: "after-final-journal", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_FINAL_JOURNAL=4", code: 99},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "authority-windows.db")
			command := exec.Command(os.Args[0], "-test.run=^TestMigrationTestFailpointCrashHelper$", "-test.count=1")
			command.Env = append(os.Environ(), "TALARIA_TEST_MIGRATION_CRASH_HELPER=1", "TALARIA_TEST_MIGRATION_CRASH_PATH="+path, test.variable)
			runErr := command.Run()
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != test.code {
				t.Fatalf("authority crash helper error = %v, want exit %d", runErr, test.code)
			}
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if test.name != "after-final-journal" {
				if err := validateOrdinaryOpenSchema(context.Background(), database); err == nil {
					database.Close()
					t.Fatal("ordinary open accepted an incomplete migration authority window")
				}
			}
			if err := applyMigrations(context.Background(), database); err != nil {
				database.Close()
				t.Fatalf("authority-window recovery failed: %v", err)
			}
			if err := validateOrdinaryOpenSchema(context.Background(), database); err != nil {
				database.Close()
				t.Fatalf("recovered authority window is not ordinary-open ready: %v", err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationDirtyRecoveryAfterProcessDeath(t *testing.T) {
	for _, test := range []struct {
		name, variable string
		seed           uint
		code           int
	}{
		{name: "migration-2-before-commit", variable: "TALARIA_TEST_MIGRATION_KILL_BEFORE_COMMIT=2", seed: 1, code: 93},
		{name: "migration-2-applied-ddl-before-clean", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_DDL=2", seed: 1, code: 94},
		{name: "migration-3-before-commit", variable: "TALARIA_TEST_MIGRATION_KILL_BEFORE_COMMIT=3", seed: 2, code: 93},
		{name: "migration-3-applied-ddl-before-clean", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_DDL=3", seed: 2, code: 94},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "process-death.db")
			seedMigrationDatabase(t, path, test.seed)
			command := exec.Command(os.Args[0], "-test.run=^TestMigrationTestFailpointCrashHelper$", "-test.count=1")
			command.Env = append(os.Environ(), "TALARIA_TEST_MIGRATION_CRASH_HELPER=1", "TALARIA_TEST_MIGRATION_CRASH_PATH="+path, test.variable)
			err := command.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != test.code {
				t.Fatalf("crash helper error = %v, want exit %d", err, test.code)
			}
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := latestMigrationEvidence(context.Background(), database)
			if err != nil {
				database.Close()
				t.Fatal(err)
			}
			if evidence.FailureStage != migrationStageStarted || evidence.RunID == "" || !evidence.Dirty {
				database.Close()
				t.Fatalf("process-death evidence = %+v, want durable started evidence", evidence)
			}
			if err := applyMigrations(context.Background(), database); err != nil {
				database.Close()
				t.Fatalf("recovery after process death: %v", err)
			}
			var version int
			var dirty bool
			if err := database.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
				database.Close()
				t.Fatal(err)
			}
			if version != int(embeddedMigrationTarget) || dirty {
				database.Close()
				t.Fatalf("recovered state = version %d dirty %v", version, dirty)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationDirtyRecoveryAfterProcessDeathLegacyJournal(t *testing.T) {
	for _, test := range []struct {
		name, variable string
		version        uint
		code           int
	}{
		{name: "legacy-v1-to-v2-before-commit", variable: "TALARIA_TEST_MIGRATION_KILL_BEFORE_COMMIT=2", version: 2, code: 93},
		{name: "legacy-v1-to-v2-after-ddl-before-clean", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_DDL=2", version: 2, code: 94},
		{name: "legacy-v2-to-v3-before-commit", variable: "TALARIA_TEST_MIGRATION_KILL_BEFORE_COMMIT=3", version: 3, code: 93},
		{name: "legacy-v2-to-v3-after-ddl-before-clean", variable: "TALARIA_TEST_MIGRATION_KILL_AFTER_DDL=3", version: 3, code: 94},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-process-death.db")
			seedMigrationDatabase(t, path, test.version-1)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestMigrationTestFailpointCrashHelper$", "-test.count=1")
			command.Env = append(os.Environ(), "TALARIA_TEST_MIGRATION_CRASH_HELPER=1", "TALARIA_TEST_MIGRATION_CRASH_PATH="+path, test.variable)
			runErr := command.Run()
			var exitErr *exec.ExitError
			if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != test.code {
				t.Fatalf("legacy crash helper error = %v, want exit %d", runErr, test.code)
			}
			database, err = sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := latestMigrationEvidence(context.Background(), database)
			if err != nil {
				database.Close()
				t.Fatal(err)
			}
			if evidence.FailureStage != migrationStageStarted || evidence.RunID == "" || !evidence.Dirty {
				database.Close()
				t.Fatalf("legacy process-death evidence = %+v", evidence)
			}
			if err := applyMigrations(context.Background(), database); err != nil {
				database.Close()
				t.Fatalf("legacy recovery after process death: %v", err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationDirtyRecoveryCrashPositions(t *testing.T) {
	for _, test := range []struct {
		name  string
		phase string
	}{
		{name: "migration-2-before-commit", phase: migrationStageRollbackBeforeCommit},
		{name: "migration-2-after-ddl-before-clean", phase: migrationStageAppliedDDLBeforeClean},
		{name: "migration-3-before-commit", phase: migrationStageRollbackBeforeCommit},
		{name: "migration-3-after-ddl-before-clean", phase: migrationStageAppliedDDLBeforeClean},
	} {
		t.Run(test.name, func(t *testing.T) {
			failureVersion := uint(2)
			if strings.HasPrefix(test.name, "migration-3-") {
				failureVersion = 3
			}
			database, err := sql.Open("sqlite", "file:"+t.TempDir()+"/crash.db")
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			failpoints := testMigrationFailpoints{}
			if test.phase == migrationStageRollbackBeforeCommit {
				failpoints.BeforeRun = func(version uint) error {
					if version == failureVersion {
						return errors.New("injected rollback-before-commit")
					}
					return nil
				}
			} else {
				failpoints.AfterCleanVersion = func(version uint) error {
					if version == failureVersion {
						return errors.New("injected applied-ddl-before-clean")
					}
					return nil
				}
			}
			if err := applyMigrationsWithTestOptions(context.Background(), database, testMigrationOptions{failpoints: failpoints}); err == nil {
				t.Fatal("injected migration failure unexpectedly succeeded")
			}
			var stage string
			if err := database.QueryRow("SELECT failure_stage FROM migration_journal ORDER BY id DESC LIMIT 1").Scan(&stage); err != nil {
				t.Fatal(err)
			}
			if stage != test.phase {
				t.Fatalf("journal failure stage = %q, want %q", stage, test.phase)
			}
			if err := applyMigrations(context.Background(), database); err != nil {
				t.Fatalf("durable recovery failed: %v", err)
			}
		})
	}
}
