package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderConfigInstallerCreatesDefaultOnlyWhenAbsent(t *testing.T) {
	root := secureCodexTestDirectory(t)
	path := filepath.Join(root, "providers.toml")
	request := SetupRequest{ProviderConfigPath: path}
	installer := NewProviderConfigInstaller()
	changes, err := installer.Plan(context.Background(), request, strings.Repeat("d", 64), false)
	if err != nil || len(changes) != 1 {
		t.Fatalf("plan=%+v err=%v", changes, err)
	}
	if err := installer.Install(context.Background(), request, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model = "gpt-5.6-luna"`) {
		t.Fatalf("default provider config=%s", data)
	}
	custom := []byte("version = 1\nenabled = false\nchain = []\n")
	if err := os.WriteFile(path, custom, ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	if changes, err := installer.Plan(context.Background(), request, strings.Repeat("d", 64), false); err != nil || len(changes) != 0 {
		t.Fatalf("existing provider plan=%+v err=%v", changes, err)
	}
}
