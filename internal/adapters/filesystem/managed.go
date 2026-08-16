package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

const (
	ManagedDirectoryMode fs.FileMode = 0o700
	ManagedFileMode      fs.FileMode = 0o600
)

// PathPolicy is the concrete T9 implementation consumed by SQLite and later
// composition tasks. Existing files are checked without following symlinks;
// missing final files remain valid so an owning adapter can create them.
type PathPolicy struct{}

func NewPathPolicy() PathPolicy { return PathPolicy{} }

func NewManagedPathPolicy() PathPolicy { return NewPathPolicy() }

func (PathPolicy) ValidateManagedPath(path string, finalMode fs.FileMode) error {
	if path == "" || finalMode.Perm() != ManagedFileMode || finalMode&fs.ModeType != 0 {
		return ErrUnsafePath
	}
	if err := (PathPolicy{}).ValidateManagedPathParent(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return classifyPathError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return ErrUnsafePath
	}
	if !info.Mode().IsRegular() || !currentUserOwns(info) {
		return ErrUnsafePath
	}
	if info.Mode().Perm() != finalMode.Perm() {
		return ErrInvalidMode
	}
	return nil
}

func (PathPolicy) ValidateManagedPathParent(path string) error {
	if path == "" {
		return ErrUnsafePath
	}
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return classifyPathError(err)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || info.Mode().Perm() != ManagedDirectoryMode || !currentUserOwns(info) {
		return ErrUnsafePath
	}
	return nil
}

// EnsureManagedDirectory creates exactly one managed directory if absent.
// Parent directories must already be controlled by the caller; this avoids a
// recursive mkdir silently claiming an unreviewed path tree.
func EnsureManagedDirectory(path string) error {
	if path == "" {
		return ErrUnsafePath
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != ManagedDirectoryMode || !currentUserOwns(info) {
			return ErrUnsafePath
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return classifyPathError(err)
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return classifyPathError(err)
	}
	if parentInfo.Mode()&fs.ModeSymlink != 0 || !parentInfo.IsDir() || !currentUserOwns(parentInfo) {
		return ErrUnsafePath
	}
	if err := os.Mkdir(path, ManagedDirectoryMode); err != nil {
		return classifyPathError(err)
	}
	if err := os.Chmod(path, ManagedDirectoryMode); err != nil {
		return classifyPathError(err)
	}
	return nil
}

type ManagedFileStore struct{ policy PathPolicy }

func NewManagedFileStore(policies ...PathPolicy) *ManagedFileStore {
	policy := PathPolicy{}
	if len(policies) > 0 {
		policy = policies[0]
	}
	return &ManagedFileStore{policy: policy}
}

func New(policies ...PathPolicy) *ManagedFileStore { return NewManagedFileStore(policies...) }

func (store *ManagedFileStore) CreateTemp(ctx context.Context, directory, prefix string, mode fs.FileMode) (ports.ManagedTempFile, error) {
	if err := contextErr(ctx); err != nil {
		return ports.ManagedTempFile{}, err
	}
	if mode.Perm() != ManagedFileMode || mode&fs.ModeType != 0 {
		return ports.ManagedTempFile{}, ErrInvalidMode
	}
	if prefix == "" || strings.ContainsAny(prefix, `/\\`) {
		return ports.ManagedTempFile{}, ErrInvalidPrefix
	}
	if err := store.policy.ValidateManagedPathParent(filepath.Join(directory, "placeholder")); err != nil {
		return ports.ManagedTempFile{}, err
	}
	file, err := os.CreateTemp(directory, prefix)
	if err != nil {
		return ports.ManagedTempFile{}, classifyPathError(err)
	}
	path := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(ManagedFileMode); err != nil {
		return ports.ManagedTempFile{}, classifyPathError(err)
	}
	if err := validateRegularFile(path, ManagedFileMode, true); err != nil {
		return ports.ManagedTempFile{}, err
	}
	cleanup = false
	return ports.ManagedTempFile{Path: path, Writer: file}, nil
}

func (store *ManagedFileStore) SyncFile(ctx context.Context, path string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := validateRegularFile(path, ManagedFileMode, true); err != nil {
		return err
	}
	file, err := openManaged(path, os.O_RDONLY, 0)
	if err != nil {
		return classifyPathError(err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return classifyPathError(err)
	}
	return nil
}

func (store *ManagedFileStore) ReplaceNoFollow(ctx context.Context, tempPath, targetPath string, expected *ports.FileFingerprint) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if tempPath == "" || targetPath == "" || filepath.Clean(tempPath) == filepath.Clean(targetPath) {
		return ErrUnsafePath
	}
	if filepath.Clean(filepath.Dir(tempPath)) != filepath.Clean(filepath.Dir(targetPath)) {
		return ErrDifferentDirectory
	}
	if err := validateRegularFile(tempPath, ManagedFileMode, true); err != nil {
		return err
	}
	if err := store.SyncFile(ctx, tempPath); err != nil {
		return err
	}
	targetInfo, err := os.Lstat(targetPath)
	targetExists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return classifyPathError(err)
	}
	if targetExists {
		if targetInfo.Mode()&fs.ModeSymlink != 0 || !targetInfo.Mode().IsRegular() || !currentUserOwns(targetInfo) {
			return ErrUnsafePath
		}
		if expected == nil {
			return ErrTargetExists
		}
		actual, err := store.FingerprintNoFollow(ctx, targetPath)
		if err != nil {
			return err
		}
		if !sameFingerprint(actual, *expected) {
			return ErrFingerprintMismatch
		}
	} else if expected != nil {
		return ErrTargetMissing
	}
	if expected == nil {
		// Link is atomic and never replaces an existing target. It is the
		// portable no-clobber equivalent of renameat2(RENAME_NOREPLACE) for
		// same-directory filesystems supported by this adapter.
		if err := os.Link(tempPath, targetPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				return ErrTargetExists
			}
			return classifyPathError(err)
		}
		if err := os.Remove(tempPath); err != nil {
			return classifyPathError(err)
		}
	} else if err := os.Rename(tempPath, targetPath); err != nil {
		return classifyPathError(err)
	}
	if err := store.SyncFile(ctx, targetPath); err != nil {
		return err
	}
	return store.SyncParent(ctx, targetPath)
}

func (store *ManagedFileStore) SyncParent(ctx context.Context, path string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := store.policy.ValidateManagedPathParent(filepath.Join(parent, "placeholder")); err != nil {
		return err
	}
	directory, err := os.Open(parent)
	if err != nil {
		return classifyPathError(err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return classifyPathError(err)
	}
	return nil
}

func (store *ManagedFileStore) CleanupStaleTemps(ctx context.Context, directory, prefix string, olderThan time.Time) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if prefix == "" || strings.ContainsAny(prefix, `/\\`) {
		return ErrInvalidPrefix
	}
	if err := store.policy.ValidateManagedPathParent(filepath.Join(directory, "placeholder")); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return classifyPathError(err)
	}
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return classifyPathError(err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ErrUnsafePath
		}
		if !info.Mode().IsRegular() || !currentUserOwns(info) || info.Mode().Perm() != ManagedFileMode {
			return ErrUnsafePath
		}
		if info.ModTime().Before(olderThan) {
			if err := os.Remove(path); err != nil {
				return classifyPathError(err)
			}
		}
	}
	return store.SyncParent(ctx, filepath.Join(directory, "placeholder"))
}

func (store *ManagedFileStore) FingerprintNoFollow(ctx context.Context, path string) (ports.FileFingerprint, error) {
	if err := contextErr(ctx); err != nil {
		return ports.FileFingerprint{}, err
	}
	if err := validateRegularFile(path, ManagedFileMode, true); err != nil {
		return ports.FileFingerprint{}, err
	}
	file, err := openManaged(path, os.O_RDONLY, 0)
	if err != nil {
		return ports.FileFingerprint{}, classifyPathError(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ports.FileFingerprint{}, classifyPathError(err)
	}
	info, err := file.Stat()
	if err != nil {
		return ports.FileFingerprint{}, classifyPathError(err)
	}
	digest := hash.Sum(nil)
	return ports.FileFingerprint{SHA256Hex: hex.EncodeToString(digest), Size: info.Size(), Mode: info.Mode().Perm()}, nil
}

func validateRegularFile(path string, mode fs.FileMode, requireParent bool) error {
	if path == "" {
		return ErrUnsafePath
	}
	if requireParent {
		if err := (PathPolicy{}).ValidateManagedPathParent(path); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return classifyPathError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return ErrUnsafePath
	}
	if !info.Mode().IsRegular() {
		return ErrNotRegular
	}
	if !currentUserOwns(info) {
		return ErrUnsafePath
	}
	if info.Mode().Perm() != mode.Perm() {
		return ErrInvalidMode
	}
	return nil
}

func sameFingerprint(actual, expected ports.FileFingerprint) bool {
	return actual.SHA256Hex == expected.SHA256Hex && actual.Size == expected.Size && actual.Mode.Perm() == expected.Mode.Perm()
}

func validateNoSymlinkAncestors(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ErrUnsafePath
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return classifyPathError(err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ErrUnsafePath
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func classifyPathError(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ErrTargetMissing
	case errors.Is(err, os.ErrPermission):
		return ErrUnsafePath
	default:
		return fmt.Errorf("managed filesystem operation failed: %w", err)
	}
}

var _ ports.ManagedFileStore = (*ManagedFileStore)(nil)
var _ ports.ManagedPathPolicy = PathPolicy{}
