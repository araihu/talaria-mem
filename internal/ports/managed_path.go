package ports

import "io/fs"

// ManagedPathPolicy is the replaceable storage-boundary contract for paths
// that hold managed database or receipt bytes. The ports package specifies
// the seam only; filesystem ownership, mode, and no-follow policy belong to a
// concrete adapter so they can be injected and tested independently.
type ManagedPathPolicy interface {
	ValidateManagedPath(path string, finalMode fs.FileMode) error
	ValidateManagedPathParent(path string) error
}

// ManagedDatabasePreparer is an explicit T14/T13 composition dependency. The
// T3 SQLite adapter never discovers a filesystem implementation through
// package initialization or mutable global state.
type ManagedDatabasePreparer func(path string, policy ManagedPathPolicy) error
