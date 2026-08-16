package sqlite

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type rejectingManagedPathPolicy struct{}

func (rejectingManagedPathPolicy) ValidateManagedPath(string, fs.FileMode) error {
	return ports.NewPortContractError("injected path policy rejection")
}

func (rejectingManagedPathPolicy) ValidateManagedPathParent(string) error {
	return ports.NewPortContractError("injected parent policy rejection")
}

func TestSQLiteOpenUsesInjectedManagedPathPolicy(t *testing.T) {
	if _, err := OpenWithManagedPathPolicy(t.Context(), filepath.Join(t.TempDir(), "database.db"), rejectingManagedPathPolicy{}); err == nil {
		t.Fatal("injected managed path policy was not consulted")
	}
}

func TestSQLiteMaintenanceUsesInjectedManagedPathPolicy(t *testing.T) {
	if _, err := OpenForMaintenanceWithManagedPathPolicy(t.Context(), filepath.Join(t.TempDir(), "database.db"), rejectingManagedPathPolicy{}, testPrepareManagedDatabaseFile); err == nil {
		t.Fatal("injected maintenance path policy was not consulted")
	}
}
