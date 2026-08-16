package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedFilesystemCreateFingerprintAndNoClobber(t *testing.T) {
	directory := secureDirectory(t)
	store := NewManagedFileStore()
	temporary, err := store.CreateTemp(context.Background(), directory, ".talaria-test-", fs.FileMode(ManagedFileMode))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.Writer.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := temporary.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "projection.md")
	if err := store.ReplaceNoFollow(context.Background(), temporary.Path, target, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(temporary.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary path after replace = %v", err)
	}
	fingerprint, err := store.FingerprintNoFollow(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint.Size != int64(len("first")) || fingerprint.Mode.Perm() != ManagedFileMode {
		t.Fatalf("fingerprint = %+v", fingerprint)
	}
	second, err := store.CreateTemp(context.Background(), directory, ".talaria-test-", fs.FileMode(ManagedFileMode))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Writer.Close(); _ = os.Remove(second.Path) }()
	if err := store.ReplaceNoFollow(context.Background(), second.Path, target, nil); !errors.Is(err, ErrTargetExists) {
		t.Fatalf("no-clobber error = %v", err)
	}
}

func TestManagedFilesystemForcedReplacementRequiresFingerprint(t *testing.T) {
	directory := secureDirectory(t)
	store := NewManagedFileStore()
	first := filepath.Join(directory, "target")
	if err := AtomicWriteNoFollow(context.Background(), first, []byte("one")); err != nil {
		t.Fatal(err)
	}
	old, err := store.FingerprintNoFollow(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateTemp(context.Background(), directory, ".talaria-test-", fs.FileMode(ManagedFileMode))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Writer.Write([]byte("two")); err != nil {
		t.Fatal(err)
	}
	if err := second.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	wrong := old
	wrong.SHA256Hex = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := store.ReplaceNoFollow(context.Background(), second.Path, first, &wrong); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("wrong fingerprint error = %v", err)
	}
	if err := store.ReplaceNoFollow(context.Background(), second.Path, first, &old); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(first)
	if err != nil || string(content) != "two" {
		t.Fatalf("replacement content = %q, error = %v", content, err)
	}
}

func TestManagedFilesystemRejectsSymlinksAndUnsafeModes(t *testing.T) {
	directory := secureDirectory(t)
	store := NewManagedFileStore()
	realPath := filepath.Join(directory, "real")
	linkPath := filepath.Join(directory, "link")
	if err := os.WriteFile(realPath, []byte("secret"), ManagedFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FingerprintNoFollow(context.Background(), linkPath); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink fingerprint error = %v", err)
	}
	unsafe := filepath.Join(directory, "unsafe")
	if err := os.WriteFile(unsafe, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FingerprintNoFollow(context.Background(), unsafe); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("unsafe mode error = %v", err)
	}
}

func TestManagedFilesystemStaleCleanupAndParentSync(t *testing.T) {
	directory := secureDirectory(t)
	store := NewManagedFileStore()
	old, err := store.CreateTemp(context.Background(), directory, ".stale-", fs.FileMode(ManagedFileMode))
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old.Path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	fresh, err := store.CreateTemp(context.Background(), directory, ".stale-", fs.FileMode(ManagedFileMode))
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupStaleTemps(context.Background(), directory, ".stale-", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old temp still exists: %v", err)
	}
	if _, err := os.Stat(fresh.Path); err != nil {
		t.Fatalf("fresh temp missing: %v", err)
	}
}

func TestManagedFilesystemContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := NewManagedFileStore()
	if _, err := store.CreateTemp(ctx, secureDirectory(t), ".cancel-", fs.FileMode(ManagedFileMode)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create error = %v", err)
	}
}

func secureDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, ManagedDirectoryMode); err != nil {
		t.Fatal(err)
	}
	return directory
}
