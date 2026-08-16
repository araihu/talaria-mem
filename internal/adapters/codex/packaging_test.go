package codex

import (
	"os"
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
	if !strings.Contains(script, "http://127.0.0.1") || !strings.Contains(script, "--data-binary @-") {
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
