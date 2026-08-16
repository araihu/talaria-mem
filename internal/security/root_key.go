package security

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// RootKeySize is deliberately independent of every derived key size. The
	// root is never used directly as an authentication or digest key.
	RootKeySize       = 32
	RootKeyVersion    = uint32(1)
	RootKeyFileMode   = os.FileMode(0o600)
	ManagedDirMode    = os.FileMode(0o700)
	ProtectedFileMode = os.FileMode(0o600)
)

var (
	ErrRootKeyMissing    = errors.New("root key unavailable")
	ErrRootKeyCorrupt    = errors.New("root key invalid")
	ErrRootKeyUnsafe     = errors.New("root key path unsafe")
	ErrRootKeyExists     = errors.New("root key already exists")
	ErrRootKeyPermission = errors.New("root key permission denied")
)

var zeroRootKey [RootKeySize]byte

// RootKey is an immutable copy of the installation key. Bytes returns a copy
// so callers cannot mutate the key held by a deriver after construction.
type RootKey struct {
	version uint32
	bytes   [RootKeySize]byte
}

func NewRootKey(value []byte, version uint32) (RootKey, error) {
	if version != RootKeyVersion || len(value) != RootKeySize || bytes.Equal(value, zeroRootKey[:]) {
		return RootKey{}, ErrRootKeyCorrupt
	}
	var key RootKey
	key.version = version
	copy(key.bytes[:], value)
	return key, nil
}

func (key RootKey) Version() uint32 { return key.version }

func (key RootKey) Bytes() []byte {
	value := make([]byte, len(key.bytes))
	copy(value, key.bytes[:])
	return value
}

func (key RootKey) valid() bool {
	return key.version == RootKeyVersion
}

// GenerateRootKey generates a fresh key in memory. It does not persist it.
func GenerateRootKey() (RootKey, error) {
	var key RootKey
	key.version = RootKeyVersion
	for {
		if _, err := io.ReadFull(rand.Reader, key.bytes[:]); err != nil {
			return RootKey{}, fmt.Errorf("generate root key: %w", err)
		}
		if !bytes.Equal(key.bytes[:], zeroRootKey[:]) {
			return key, nil
		}
	}
}

// CreateRootKey is the explicit first-install operation. It never replaces an
// existing path, including an existing symlink.
func CreateRootKey(path string) (RootKey, error) {
	if err := validateProtectedParent(path); err != nil {
		return RootKey{}, err
	}
	key, err := GenerateRootKey()
	if err != nil {
		return RootKey{}, err
	}
	file, err := openProtected(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, RootKeyFileMode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return RootKey{}, ErrRootKeyExists
		}
		return RootKey{}, classifyProtectedPathError(err)
	}
	created := false
	defer func() {
		if !created {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if err := validateOpenedProtectedFile(file, -1); err != nil {
		return RootKey{}, err
	}
	if _, err := file.Write(key.bytes[:]); err != nil {
		return RootKey{}, classifyProtectedPathError(err)
	}
	if err := file.Sync(); err != nil {
		return RootKey{}, classifyProtectedPathError(err)
	}
	if err := file.Close(); err != nil {
		return RootKey{}, classifyProtectedPathError(err)
	}
	if err := syncProtectedParent(path); err != nil {
		return RootKey{}, err
	}
	created = true
	return key, nil
}

// LoadRootKey is the ordinary startup/readiness operation. Missing or
// malformed keys fail closed; callers must not call CreateRootKey as a
// recovery path because that would silently create a new trust domain.
func LoadRootKey(path string) (RootKey, error) {
	if err := validateProtectedParent(path); err != nil {
		return RootKey{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RootKey{}, ErrRootKeyMissing
		}
		return RootKey{}, classifyProtectedPathError(err)
	}
	if err := validateProtectedInfo(info, RootKeySize); err != nil {
		return RootKey{}, err
	}
	file, err := openProtected(path, os.O_RDONLY, 0)
	if err != nil {
		return RootKey{}, classifyProtectedPathError(err)
	}
	defer file.Close()
	if err := validateOpenedProtectedFile(file, RootKeySize); err != nil {
		return RootKey{}, err
	}
	var raw [RootKeySize]byte
	if _, err := io.ReadFull(file, raw[:]); err != nil {
		return RootKey{}, ErrRootKeyCorrupt
	}
	var extra [1]byte
	if count, err := file.Read(extra[:]); err != nil && !errors.Is(err, io.EOF) {
		return RootKey{}, ErrRootKeyCorrupt
	} else if count != 0 {
		return RootKey{}, ErrRootKeyCorrupt
	}
	return NewRootKey(raw[:], RootKeyVersion)
}

// ValidateRootKey performs the readiness check without returning key bytes.
func ValidateRootKey(path string) error {
	_, err := LoadRootKey(path)
	return err
}

// OpenRootKey is an explicit startup alias; it never creates a replacement.
func OpenRootKey(path string) (RootKey, error) { return LoadRootKey(path) }

func CreateRootKeyFile(path string) (RootKey, error) { return CreateRootKey(path) }
func LoadRootKeyFile(path string) (RootKey, error)   { return LoadRootKey(path) }

// RootKeyStore keeps the key path explicit and makes the distinction between
// first-install creation and ordinary startup loading visible to callers.
type RootKeyStore struct{ Path string }

func NewRootKeyStore(path string) (RootKeyStore, error) {
	if path == "" {
		return RootKeyStore{}, ErrRootKeyUnsafe
	}
	return RootKeyStore{Path: path}, nil
}

func (store RootKeyStore) Create() (RootKey, error) { return CreateRootKey(store.Path) }
func (store RootKeyStore) Load() (RootKey, error)   { return LoadRootKey(store.Path) }
func (store RootKeyStore) Ready() error             { return ValidateRootKey(store.Path) }

func validateProtectedParent(path string) error {
	if path == "" || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return ErrRootKeyUnsafe
	}
	parent := filepath.Dir(path)
	if parent == "." {
		parent, _ = filepath.Abs(parent)
	}
	info, err := os.Lstat(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrRootKeyMissing
		}
		return classifyProtectedPathError(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != ManagedDirMode || !currentUserOwns(info) {
		return ErrRootKeyUnsafe
	}
	return nil
}

func validateProtectedInfo(info os.FileInfo, size int64) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrRootKeyUnsafe
	}
	if info.Mode().Perm() != ProtectedFileMode || !currentUserOwns(info) {
		return ErrRootKeyPermission
	}
	if size >= 0 && info.Size() != size {
		return ErrRootKeyCorrupt
	}
	return nil
}

func validateOpenedProtectedFile(file *os.File, size int64) error {
	info, err := file.Stat()
	if err != nil {
		return classifyProtectedPathError(err)
	}
	return validateProtectedInfo(info, size)
}

func classifyProtectedPathError(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ErrRootKeyMissing
	case errors.Is(err, os.ErrPermission):
		return ErrRootKeyPermission
	default:
		return fmt.Errorf("protected file operation failed: %w", err)
	}
}

func syncProtectedParent(path string) error {
	parent := filepath.Dir(path)
	file, err := os.Open(parent)
	if err != nil {
		return classifyProtectedPathError(err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return classifyProtectedPathError(err)
	}
	return nil
}

// atomicReplaceProtected replaces an existing protected file only after its
// current state has been validated. It is used for token rotation; root-key
// creation intentionally uses O_EXCL and never calls this helper.
func atomicReplaceProtected(path string, value []byte, mode os.FileMode) error {
	if mode.Perm() != ProtectedFileMode || len(value) == 0 {
		return ErrRootKeyUnsafe
	}
	if err := validateProtectedParent(path); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrRootKeyMissing
		}
		return classifyProtectedPathError(err)
	} else if err := validateProtectedInfo(info, int64(len(value))); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".talaria-protected-")
	if err != nil {
		return classifyProtectedPathError(err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return classifyProtectedPathError(err)
	}
	if _, err := temp.Write(value); err != nil {
		_ = temp.Close()
		return classifyProtectedPathError(err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return classifyProtectedPathError(err)
	}
	if err := temp.Close(); err != nil {
		return classifyProtectedPathError(err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return classifyProtectedPathError(err)
	}
	return syncProtectedParent(path)
}
