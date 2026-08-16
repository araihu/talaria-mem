package runtime

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/lifecycle"
	"github.com/guilhermecastro/talaria-mem/internal/security"
)

func TestCompositionBuildsOneExplicitRuntimeGraph(t *testing.T) {
	environment := testEnvironment(t)
	if _, err := security.CreateRootKey(filepath.Join(environment.ConfigDir, "root.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := security.CreateBearerToken(filepath.Join(environment.ConfigDir, "token")); err != nil {
		t.Fatal(err)
	}
	composition, err := New(context.Background(), Config{Environment: environment, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = composition.Close() })
	if composition.Root == nil || composition.Daemon == nil || composition.HTTP == nil || composition.MCP == nil {
		t.Fatalf("incomplete graph: root=%v daemon=%v http=%v mcp=%v", composition.Root != nil, composition.Daemon != nil, composition.HTTP != nil, composition.MCP != nil)
	}
	for _, name := range []string{"daemon", "doctor", "setup", "status", "token"} {
		if _, ok := composition.Root.Registry().Lookup(name); !ok {
			t.Fatalf("command %q not registered", name)
		}
	}
}

func TestCompositionRequiresExistingRootKeyAndToken(t *testing.T) {
	environment := testEnvironment(t)
	if _, err := New(context.Background(), Config{Environment: environment}); err == nil {
		t.Fatalf("missing installation credentials accepted: %v", err)
	}
}

func testEnvironment(t *testing.T) lifecycle.Environment {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "state")
	config := filepath.Join(root, "config")
	backup := filepath.Join(state, "backups")
	for _, path := range []string{state, config, backup} {
		if err := os.Mkdir(path, lifecycle.ManagedDirectoryMode.Perm()); err != nil {
			t.Fatal(err)
		}
	}
	return lifecycle.Environment{StateDir: state, ConfigDir: config, BackupDir: backup}
}
