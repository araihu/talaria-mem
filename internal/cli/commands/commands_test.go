package commands

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
	"github.com/guilhermecastro/talaria-mem/internal/maintenance"
)

type backupCommandFixture struct{ applied string }

func (fixture *backupCommandFixture) ReconcileDryRun(context.Context) (maintenance.Receipt, string, error) {
	return maintenance.Receipt{ID: "r1", Kind: maintenance.ReceiptBackupReconcile}, "/tmp/state/receipts/r1.json", nil
}
func (fixture *backupCommandFixture) ReconcileApply(_ context.Context, path string) error {
	fixture.applied = path
	return nil
}

func TestBackupCommandHumanAndMachineReceiptOutput(t *testing.T) {
	fixture := &backupCommandFixture{}
	command := NewBackupCommands(fixture)
	var stdout, stderr bytes.Buffer
	if err := command.Reconcile(context.Background(), []string{"--dry-run"}, cli.Output{Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "receipt=/tmp/state/receipts/r1.json") || strings.Contains(stderr.String(), "content") {
		t.Fatalf("human output = %q", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if err := command.Reconcile(context.Background(), []string{"--dry-run"}, cli.Output{JSON: true, Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"receipt":"/tmp/state/receipts/r1.json"`) || stderr.Len() != 0 {
		t.Fatalf("machine output=%q stderr=%q", stdout.String(), stderr.String())
	}
	if err := command.Reconcile(context.Background(), []string{"--apply", "/tmp/state/receipts/r1.json"}, cli.Output{Stdout: &stdout, Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	if fixture.applied == "" {
		t.Fatal("apply handler was not called")
	}
}

func TestDBCommandRejectsImplicitRestoreMutation(t *testing.T) {
	command := NewDBCommands(nil, nil)
	if err := command.Run(context.Background(), []string{"restore", "--backup", "id"}, cli.Output{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}); err == nil {
		t.Fatal("restore without explicit dependency did not fail closed")
	}
}

func TestMaintenanceCommandsRegisterExplicitly(t *testing.T) {
	registry := cli.NewRegistry()
	if err := RegisterDB(registry, NewDBCommands(nil, nil)); err != nil {
		t.Fatal(err)
	}
	if err := RegisterScanner(registry, &ScannerCommands{}); err != nil {
		t.Fatal(err)
	}
	if got := registry.Names(); len(got) != 2 || got[0] != "db" || got[1] != "scanner" {
		t.Fatalf("registered names=%v", got)
	}
}

type daemonCommandFixture struct{ calls int }

func (fixture *daemonCommandFixture) Run(context.Context) error {
	fixture.calls++
	return nil
}

func TestRegisteredCommandUsesCobraFlags(t *testing.T) {
	fixture := &daemonCommandFixture{}
	registry := cli.NewRegistry()
	if err := RegisterDaemon(registry, NewDaemonCommands(fixture)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootConfig{Registry: registry, Stdout: &stdout, Stderr: &stderr})
	if code := root.Execute(context.Background(), []string{"daemon", "--foreground", "--json"}); code != cli.ExitSuccess {
		t.Fatalf("daemon exit=%d stderr=%q", code, stderr.String())
	}
	if fixture.calls != 1 || !strings.Contains(stdout.String(), `"status":"stopped"`) {
		t.Fatalf("calls=%d stdout=%q", fixture.calls, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

type setupCommandFixture struct {
	request lifecycle.SetupRequest
}

func (fixture *setupCommandFixture) Plan(_ context.Context, request lifecycle.SetupRequest) (lifecycle.SetupResult, error) {
	fixture.request = request
	return lifecycle.SetupResult{Version: "test"}, nil
}

func (fixture *setupCommandFixture) Apply(_ context.Context, request lifecycle.SetupRequest) (lifecycle.SetupResult, error) {
	fixture.request = request
	return lifecycle.SetupResult{Version: "test"}, nil
}

func TestSetupCobraForwardsCodexHooksPath(t *testing.T) {
	fixture := &setupCommandFixture{}
	registry := cli.NewRegistry()
	if err := RegisterSetup(registry, NewSetupCommands(fixture, lifecycle.SetupRequest{})); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootConfig{Registry: registry, Stdout: &stdout, Stderr: &stderr})
	if code := root.Execute(context.Background(), []string{"setup", "codex", "--dry-run", "--codex-hooks", "/tmp/codex/hooks.json"}); code != cli.ExitSuccess {
		t.Fatalf("setup exit=%d stderr=%q", code, stderr.String())
	}
	if fixture.request.CodexHooksPath != "/tmp/codex/hooks.json" {
		t.Fatalf("codex hooks path=%q", fixture.request.CodexHooksPath)
	}
}
