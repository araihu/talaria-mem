package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// SQL and WithTx exist only in test compilation. Production packages cannot
// obtain an unrestricted raw handle; repository tests remain able to inspect
// schema and inject rollback/failure fixtures inside this package.
func (database *DB) SQL() *sql.DB { return database.sql }

func (database *DB) WithTx(ctx context.Context, operation func(*sql.Tx) error) error {
	return database.withTx(ctx, operation)
}

// testManagedPathPolicy is deliberately T3-local. Concrete owner/mode/no-
// follow enforcement belongs to T9; SQLite tests only prove that explicit
// replaceable dependencies are consumed.
type testManagedPathPolicy struct{}

func (testManagedPathPolicy) ValidateManagedPath(path string, finalMode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != finalMode.Perm() {
		return ports.NewPortContractError("test managed path rejected")
	}
	return nil
}

func (testManagedPathPolicy) ValidateManagedPathParent(path string) error {
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ports.NewPortContractError("test managed parent rejected")
	}
	return nil
}

func testPrepareManagedDatabaseFile(path string, policy ports.ManagedPathPolicy) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := policy.ValidateManagedPathParent(path); err != nil {
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
