package codex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionStartPackagingIsLoopbackOnlyAndDoesNotInstall(t *testing.T) {
	path := filepath.Join("..", "..", "..", "packaging", "codex", "session-start.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if !strings.Contains(script, "http://127.0.0.1") || !strings.Contains(script, "--data-binary @-") || !strings.Contains(script, "--write-out") {
		t.Fatal("hook does not use literal loopback stdin transport")
	}
	if strings.Contains(script, "curl -L") || strings.Contains(script, "--location") || strings.Contains(script, "\ninstall ") || strings.Contains(script, "\nmv ") {
		t.Fatal("hook contains a download or binary replacement path")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("hook is not executable: mode=%o", info.Mode().Perm())
	}
}

func TestSessionStartHookDoesNotEchoTokenOnTransportFailure(t *testing.T) {
	path := filepath.Join("..", "..", "..", "packaging", "codex", "session-start.sh")
	root := t.TempDir()
	tokenPath := filepath.Join(root, "token")
	canary := "SHELL_BEARER_CANARY_MUST_NOT_APPEAR"
	if err := os.WriteFile(tokenPath, []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", path)
	command.Dir = root
	command.Env = append(os.Environ(),
		"HOME="+root,
		"TALARIA_CONFIG_DIR="+root,
		"TALARIA_TOKEN_FILE="+tokenPath,
		"TALARIA_ENDPOINT=http://127.0.0.1:1",
		"TMPDIR="+root,
	)
	command.Stdin = strings.NewReader(`{"session_id":"s","cwd":"/tmp","hook_event_name":"SessionStart"}`)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("hook unexpectedly succeeded against closed port")
	}
	if strings.Contains(string(output), canary) {
		t.Fatal("hook transport diagnostic echoed bearer token")
	}
}
