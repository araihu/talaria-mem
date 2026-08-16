package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// AtomicWriteNoFollow writes managed bytes through a same-directory temporary
// file and a no-clobber replacement. It is intended for newly-created output;
// callers that replace an existing target must provide its fingerprint through
// ManagedFileStore.ReplaceNoFollow.
func AtomicWriteNoFollow(ctx context.Context, path string, value []byte) error {
	store := NewManagedFileStore()
	temporary, err := store.CreateTemp(ctx, filepath.Dir(path), ".talaria-atomic-", fs.FileMode(ManagedFileMode))
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporary.Path)
		}
	}()
	if _, err := temporary.Writer.Write(value); err != nil {
		_ = temporary.Writer.Close()
		return errors.New("managed atomic write failed")
	}
	if err := temporary.Writer.Close(); err != nil {
		return errors.New("managed atomic write failed")
	}
	if err := store.ReplaceNoFollow(ctx, temporary.Path, path, nil); err != nil {
		return err
	}
	keep = true
	return nil
}
