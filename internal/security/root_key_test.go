package security

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRootKeyCreateLoadAndReadiness(t *testing.T) {
	directory := secureTestDirectory(t)
	path := filepath.Join(directory, "root.key")
	created, err := CreateRootKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if created.Version() != RootKeyVersion || len(created.Bytes()) != RootKeySize {
		t.Fatalf("created key metadata invalid")
	}
	loaded, err := LoadRootKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.Bytes()) != string(created.Bytes()) {
		t.Fatal("loaded key differs")
	}
	if err := ValidateRootKey(path); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateRootKey(path); !errors.Is(err, ErrRootKeyExists) {
		t.Fatalf("second creation error = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != RootKeyFileMode.Perm() {
		t.Fatalf("root key mode = %o", info.Mode().Perm())
	}
}

func TestRootKeyLossAndCorruptionFailClosed(t *testing.T) {
	directory := secureTestDirectory(t)
	path := filepath.Join(directory, "root.key")
	if _, err := LoadRootKey(path); !errors.Is(err, ErrRootKeyMissing) {
		t.Fatalf("missing key error = %v", err)
	}
	if err := os.WriteFile(path, []byte("short"), RootKeyFileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRootKey(path); !errors.Is(err, ErrRootKeyCorrupt) {
		t.Fatalf("corrupt key error = %v", err)
	}
}

func TestRootKeyRejectsSymlink(t *testing.T) {
	directory := secureTestDirectory(t)
	target := filepath.Join(directory, "real.key")
	link := filepath.Join(directory, "root.key")
	if _, err := CreateRootKey(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRootKey(link); !errors.Is(err, ErrRootKeyUnsafe) {
		t.Fatalf("symlink error = %v", err)
	}
}

func secureTestDirectory(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, ManagedDirMode); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestNewRootKeyRejectsWrongVersionAndSize(t *testing.T) {
	if _, err := NewRootKey(make([]byte, RootKeySize), RootKeyVersion); !errors.Is(err, ErrRootKeyCorrupt) {
		t.Fatalf("zero key error = %v", err)
	}
	if _, err := NewRootKey(make([]byte, RootKeySize), RootKeyVersion+1); !errors.Is(err, ErrRootKeyCorrupt) {
		t.Fatalf("wrong version error = %v", err)
	}
	if _, err := NewRootKey(make([]byte, RootKeySize-1), RootKeyVersion); !errors.Is(err, ErrRootKeyCorrupt) {
		t.Fatalf("wrong size error = %v", err)
	}
}
