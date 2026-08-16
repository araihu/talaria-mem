package sqlite

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type cp1A5FakePathPolicy struct{}

func (cp1A5FakePathPolicy) ValidateManagedPath(path string, finalMode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != finalMode.Perm() {
		return ports.NewPortContractError("cp1-a-5 fake path rejected")
	}
	return nil
}

func (cp1A5FakePathPolicy) ValidateManagedPathParent(path string) error {
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ports.NewPortContractError("cp1-a-5 fake parent rejected")
	}
	return nil
}

func cp1A5FakePrepare(path string, policy ports.ManagedPathPolicy) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return policy.ValidateManagedPath(path, 0o600)
}

func TestSQLiteUsesExplicitFilesystemPortInjection(t *testing.T) {
	called := false
	prepare := func(path string, policy ports.ManagedPathPolicy) error {
		called = true
		return cp1A5FakePrepare(path, policy)
	}
	database, err := OpenForMaintenanceWithManagedPathPolicy(
		context.Background(), filepath.Join(t.TempDir(), "explicit.db"), cp1A5FakePathPolicy{}, prepare,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if !called {
		t.Fatal("explicit managed database preparer was not invoked")
	}
}
