package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexMCPInstallerPreservesConfigAndOwnsExactBlock(t *testing.T) {
	root := secureCodexTestDirectory(t)
	path := filepath.Join(root, "config.toml")
	original := []byte("model = \"keep\"\n\n[other]\nvalue = true\n")
	if err := os.WriteFile(path, original, ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{CodexConfigPath: path, BinaryPath: "/tmp/talaria-mem", Endpoint: "127.0.0.1:7437"}
	installer := NewCodexMCPInstaller()
	if err := installer.Install(context.Background(), request, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[mcp_servers.talaria_mem]") || !strings.Contains(string(data), `args = ["mcp", "proxy"]`) || !strings.Contains(string(data), `command = "/tmp/talaria-mem"`) || !strings.Contains(string(data), `model = "keep"`) {
		t.Fatalf("config=%s", data)
	}
	if err := installer.Install(context.Background(), request, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := installer.Remove(context.Background(), request, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("unrelated config changed: %s", data)
	}
}

func TestCodexMCPInstallerRejectsCollision(t *testing.T) {
	root := secureCodexTestDirectory(t)
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, []byte("[mcp_servers.talaria_mem]\ncommand = \"user-command\"\n"), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	request := SetupRequest{CodexConfigPath: path, BinaryPath: "/tmp/talaria-mem", Endpoint: "127.0.0.1:7437"}
	if err := NewCodexMCPInstaller().Install(context.Background(), request, strings.Repeat("c", 64)); err == nil {
		t.Fatal("MCP collision accepted")
	}
}
