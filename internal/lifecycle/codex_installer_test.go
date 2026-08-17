package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexHookInstallerIsIdempotentAndCollisionSafe(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "session-start.sh")
	request := SetupRequest{HookPath: hook}
	installer := NewCodexHookInstaller()
	if err := installer.Install(context.Background(), request, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(hook)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("hook mode=%o symlink=%t", info.Mode().Perm(), info.Mode()&os.ModeSymlink != 0)
	}
	if err := installer.Install(context.Background(), request, "fingerprint"); err != nil {
		t.Fatalf("idempotent install: %v", err)
	}
	if err := os.WriteFile(hook, []byte("user hook\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := installer.Install(context.Background(), request, "fingerprint"); !errors.Is(err, ErrSetupCollision) {
		t.Fatalf("modified hook install error=%v, want collision", err)
	}
	if err := installer.Remove(context.Background(), request, "fingerprint"); !errors.Is(err, ErrSetupCollision) {
		t.Fatalf("modified hook removal error=%v, want collision", err)
	}
}
