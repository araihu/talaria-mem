package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/maintenance"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

func testEnvironment(t *testing.T) Environment {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	config := filepath.Join(root, "config")
	backup := filepath.Join(state, "backups")
	for _, path := range []string{state, config, backup} {
		if err := os.Mkdir(path, ManagedDirectoryMode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	return Environment{StateDir: state, ConfigDir: config, BackupDir: backup}
}

func TestEnvironmentRequiresAbsoluteExistingSafeDirectories(t *testing.T) {
	environment := testEnvironment(t)
	parsed, err := ParseEnvironment(map[string]string{
		"TALARIA_STATE_DIR":  environment.StateDir,
		"TALARIA_CONFIG_DIR": environment.ConfigDir,
		"TALARIA_BACKUP_DIR": environment.BackupDir,
	})
	if err != nil || parsed != environment {
		t.Fatalf("parsed=%+v err=%v", parsed, err)
	}
	tests := []map[string]string{
		{"TALARIA_STATE_DIR": "relative", "TALARIA_CONFIG_DIR": environment.ConfigDir, "TALARIA_BACKUP_DIR": environment.BackupDir},
		{"TALARIA_STATE_DIR": environment.StateDir + "/missing", "TALARIA_CONFIG_DIR": environment.ConfigDir, "TALARIA_BACKUP_DIR": environment.BackupDir},
	}
	for _, values := range tests {
		if _, err := ParseEnvironment(values); err == nil {
			t.Fatalf("unsafe environment accepted: %+v", values)
		}
	}
	if err := environment.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentRejectsSymlinkAndModeDrift(t *testing.T) {
	environment := testEnvironment(t)
	link := filepath.Join(filepath.Dir(environment.StateDir), "state-link")
	if err := os.Symlink(environment.StateDir, link); err != nil {
		t.Fatal(err)
	}
	environment.StateDir = link
	if err := environment.Validate(); err == nil {
		t.Fatal("symlink accepted")
	}
	environment = testEnvironment(t)
	if err := os.Chmod(environment.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := environment.Validate(); err == nil {
		t.Fatal("mode drift accepted")
	}
}

func TestReadinessBlocksActivationPhasesAndReportsHealthSeparately(t *testing.T) {
	journal, err := newTestJournal(t, ports.ActivationActive)
	if err != nil {
		t.Fatal(err)
	}
	readiness := NewReadiness(Readiness{Activation: journal})
	ready, reason, err := readiness.Ready(context.Background())
	if err != nil || !ready || reason != "" {
		t.Fatalf("active readiness=%t reason=%q err=%v", ready, reason, err)
	}
	if err := journal.SetBlockers("projection drift"); err != nil {
		t.Fatal(err)
	}
	ready, reason, err = readiness.Ready(context.Background())
	if err != nil || ready || reason != "projection drift" {
		t.Fatalf("blocked readiness=%t reason=%q err=%v", ready, reason, err)
	}
}

func TestDaemonHealthDoesNotImplyReadiness(t *testing.T) {
	daemon, err := NewDaemon(DaemonConfig{Address: "127.0.0.1:7437", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})})
	if err != nil || !daemon.Health() {
		t.Fatalf("daemon=%v err=%v", daemon, err)
	}
	if _, err := NewDaemon(DaemonConfig{Address: "0.0.0.0:7437", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}); !errors.Is(err, ErrDaemonAddress) {
		t.Fatalf("non-loopback error=%v", err)
	}
}

func newTestJournal(t *testing.T, phase ports.ActivationPhase) (*maintenance.MemoryActivationJournal, error) {
	t.Helper()
	record := maintenance.DefaultActivationRecord()
	record.Phase = phase
	journal, err := maintenance.NewMemoryActivationJournal(record)
	if err != nil {
		return nil, err
	}
	return journal, nil
}
