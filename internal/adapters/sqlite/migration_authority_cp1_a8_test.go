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

func TestMigrationCP1A8CrashHelper(t *testing.T) {
	if os.Getenv("TALARIA_CP1_A8_CRASH_HELPER") != "1" {
		return
	}
	database, err := sql.Open("sqlite", "file:"+os.Getenv("TALARIA_CP1_A8_CRASH_PATH"))
	if err != nil {
		os.Exit(80)
	}
	mode := os.Getenv("TALARIA_CP1_A8_CRASH_MODE")
	switch mode {
	case "after-authority-init":
		if err := ensureCanonicalMigrationTable(context.Background(), database); err != nil {
			os.Exit(81)
		}
		os.Exit(90)
	case "after-pre-ddl-reset":
		if _, _, _, _, err := validateMigrationAuthority(context.Background(), database); err != nil {
			os.Exit(82)
		}
		if err := resetPreMigrationOneAuthority(context.Background(), database); err != nil {
			os.Exit(83)
		}
		os.Exit(91)
	default:
		os.Exit(84)
	}
}

func TestMigrationCP1A8AtomicAuthorityInitializationAfterProcessDeath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh-authority.db")
	runMigrationCP1A8CrashHelper(t, path, "after-authority-init", 90)
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertMigrationCP1A8VersionZero(t, database)
	if err := validateOrdinaryOpenSchema(context.Background(), database); err == nil {
		t.Fatal("ordinary open accepted an initialized but incomplete authority")
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("maintenance did not recover after initialization crash: %v", err)
	}
	if err := validateOrdinaryOpenSchema(context.Background(), database); err != nil {
		t.Fatalf("recovered initialization crash is not ordinary-open ready: %v", err)
	}
}

func TestMigrationCP1A8AtomicPreDDLResetAfterProcessDeath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-ddl-reset.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureCanonicalMigrationTable(context.Background(), database); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		UPDATE schema_migrations SET version = 1, dirty = 1;
		PRAGMA user_version = 0;
	`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	runMigrationCP1A8CrashHelper(t, path, "after-pre-ddl-reset", 91)
	database, err = sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertMigrationCP1A8VersionZero(t, database)
	if err := validateOrdinaryOpenSchema(context.Background(), database); err == nil {
		t.Fatal("ordinary open accepted a pre-DDL reset authority")
	}
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("maintenance did not recover after pre-DDL reset crash: %v", err)
	}
	if err := validateOrdinaryOpenSchema(context.Background(), database); err != nil {
		t.Fatalf("recovered pre-DDL reset crash is not ordinary-open ready: %v", err)
	}
}

func runMigrationCP1A8CrashHelper(t *testing.T, path, mode string, wantExit int) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestMigrationCP1A8CrashHelper$", "-test.count=1")
	command.Env = append(os.Environ(),
		"TALARIA_CP1_A8_CRASH_HELPER=1",
		"TALARIA_CP1_A8_CRASH_PATH="+path,
		"TALARIA_CP1_A8_CRASH_MODE="+mode,
	)
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != wantExit {
		t.Fatalf("%s crash helper error = %v, want exit %d", mode, err, wantExit)
	}
}

func assertMigrationCP1A8VersionZero(t *testing.T, database *sql.DB) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("authority row count after crash = %d, want 1", count)
	}
	var version int
	var dirty bool
	if err := database.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != 0 || dirty {
		t.Fatalf("authority after crash = version %d dirty %v, want 0/false", version, dirty)
	}
	var journalCount int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_journal'").Scan(&journalCount); err != nil {
		t.Fatal(err)
	}
	if journalCount != 0 {
		t.Fatalf("pre-DDL crash unexpectedly has migration journal count %d", journalCount)
	}
	var userVersion int
	if err := database.QueryRow("PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatal(err)
	}
	if userVersion != 0 {
		t.Fatalf("user_version after crash = %d, want 0", userVersion)
	}
	if snapshot := migrationAuthoritySchemaSnapshot(t, database); snapshot == "" {
		t.Fatal(fmt.Errorf("empty authority snapshot after crash"))
	}
}
