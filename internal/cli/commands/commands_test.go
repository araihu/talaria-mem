package commands

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/cli"
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
