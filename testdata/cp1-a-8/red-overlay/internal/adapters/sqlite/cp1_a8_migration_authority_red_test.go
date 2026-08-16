package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMigrationAuthorityCP1A8CrashAfterCanonicalAuthorityInitialization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authority-init.db")
	command := exec.Command(os.Args[0], "-test.run=^TestMigrationAuthorityCP1A8CrashHelper$", "-test.count=1")
	command.Env = append(os.Environ(),
		"TALARIA_CP1_A8_RED_HELPER=1",
		"TALARIA_CP1_A8_RED_PATH="+path,
	)
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 90 {
		t.Fatalf("authority initialization helper error = %v, want exit 90", err)
	}
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := applyMigrations(context.Background(), database); err != nil {
		t.Fatalf("maintenance did not recover after initialization crash: %v", err)
	}
	if err := validateOrdinaryOpenSchema(context.Background(), database); err != nil {
		t.Fatalf("recovered initialization crash is not ordinary-open ready: %v", err)
	}
}

func TestMigrationAuthorityCP1A8CrashHelper(t *testing.T) {
	if os.Getenv("TALARIA_CP1_A8_RED_HELPER") != "1" {
		return
	}
	database, err := sql.Open("sqlite", "file:"+os.Getenv("TALARIA_CP1_A8_RED_PATH"))
	if err != nil {
		os.Exit(80)
	}
	if err := ensureCanonicalMigrationTable(context.Background(), database); err != nil {
		os.Exit(81)
	}
	os.Exit(90)
}
