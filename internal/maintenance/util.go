package maintenance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/adapters/filesystem"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

const (
	ManagedDirectoryMode fs.FileMode = 0o700
	ManagedFileMode      fs.FileMode = 0o600
	ReceiptTTL                       = 15 * time.Minute
	BackupSchemaVersion              = 3
)

func operationID() string {
	// UUIDv7 is available in the pinned google/uuid module.  Falling back to a
	// random UUID keeps the maintenance identity non-content if a future module
	// removes the helper.
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

func clockNow(clock ports.Clock) time.Time {
	if clock == nil {
		return time.Now().UTC()
	}
	return clock.Now().UTC()
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

func validateMetadata(value string, max int, required bool) error {
	if required && value == "" {
		return ErrReceiptInvalid
	}
	if !utf8.ValidString(value) || len(value) > max || strings.ContainsAny(value, "\x00\r\n") {
		return ErrReceiptInvalid
	}
	return nil
}

func validateManagedDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrUnsafePath
	}
	info, err := os.Lstat(path)
	if err != nil {
		return ErrUnsafePath
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != ManagedDirectoryMode || !owns(info) {
		return ErrUnsafePath
	}
	return nil
}

func validateManagedParent(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return ErrUnsafePath
	}
	return validateManagedDirectory(filepath.Dir(path))
}

func validateManagedFile(path string) (os.FileInfo, error) {
	if err := validateManagedParent(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != ManagedFileMode || !owns(info) {
		return nil, ErrUnsafePath
	}
	return info, nil
}

func owns(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return uint64(stat.Uid) == uint64(os.Getuid())
}

func ensureDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != ManagedDirectoryMode || !owns(info) {
			return ErrUnsafePath
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if err := validateManagedDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, ManagedDirectoryMode); err != nil {
		return err
	}
	return os.Chmod(path, ManagedDirectoryMode)
}

func ensureReceiptDirectory(stateOrReceiptDir string) (string, error) {
	if stateOrReceiptDir == "" {
		return "", ErrUnsafePath
	}
	absolute, err := filepath.Abs(stateOrReceiptDir)
	if err != nil {
		return "", ErrUnsafePath
	}
	if filepath.Clean(absolute) != absolute {
		return "", ErrUnsafePath
	}
	// NewReceiptStore accepts either a state root or an explicit receipts
	// directory.  The explicit constructor is useful for tests and adapters.
	if filepath.Base(absolute) == "receipts" {
		if err := validateManagedDirectory(filepath.Dir(absolute)); err != nil {
			return "", err
		}
		if err := ensureDirectory(absolute); err != nil {
			return "", err
		}
		return absolute, nil
	}
	if err := validateManagedDirectory(absolute); err != nil {
		return "", err
	}
	receipts := filepath.Join(absolute, "receipts")
	if err := ensureDirectory(receipts); err != nil {
		return "", err
	}
	return receipts, nil
}

func fingerprint(path string) (ports.FileFingerprint, error) {
	if _, err := validateManagedFile(path); err != nil {
		return ports.FileFingerprint{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return ports.FileFingerprint{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return ports.FileFingerprint{}, err
	}
	return ports.FileFingerprint{SHA256Hex: hex.EncodeToString(hash.Sum(nil)), Size: size, Mode: ManagedFileMode}, nil
}

func readManaged(path string, value any) (ports.FileFingerprint, error) {
	if _, err := validateManagedFile(path); err != nil {
		return ports.FileFingerprint{}, err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return ports.FileFingerprint{}, err
	}
	if err := domain.RejectDuplicateJSONKeys(bytes); err != nil {
		return ports.FileFingerprint{}, ErrReceiptInvalid
	}
	if err := json.Unmarshal(bytes, value); err != nil {
		return ports.FileFingerprint{}, ErrReceiptInvalid
	}
	fp, err := fingerprint(path)
	if err != nil {
		return ports.FileFingerprint{}, err
	}
	return fp, nil
}

func writeManaged(ctx context.Context, path string, value any, expected *ports.FileFingerprint) (ports.FileFingerprint, error) {
	if err := contextErr(ctx); err != nil {
		return ports.FileFingerprint{}, err
	}
	if err := validateManagedParent(path); err != nil {
		return ports.FileFingerprint{}, fmt.Errorf("write parent: %w", err)
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return ports.FileFingerprint{}, err
	}
	store := filesystem.NewManagedFileStore()
	temporary, err := store.CreateTemp(ctx, filepath.Dir(path), ".talaria-maintenance-", ManagedFileMode)
	if err != nil {
		return ports.FileFingerprint{}, fmt.Errorf("create temp: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporary.Path)
		}
	}()
	if _, err := temporary.Writer.Write(bytes); err != nil {
		_ = temporary.Writer.Close()
		return ports.FileFingerprint{}, err
	}
	if err := temporary.Writer.Close(); err != nil {
		return ports.FileFingerprint{}, err
	}
	if err := store.ReplaceNoFollow(ctx, temporary.Path, path, expected); err != nil {
		return ports.FileFingerprint{}, fmt.Errorf("replace target: %w", err)
	}
	keep = true
	result, err := fingerprint(path)
	if err != nil {
		return ports.FileFingerprint{}, fmt.Errorf("write fingerprint: %w", err)
	}
	return result, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func hmacHex(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func secureEqualHex(left, right string) bool {
	a, errA := hex.DecodeString(left)
	b, errB := hex.DecodeString(right)
	return errA == nil && errB == nil && hmac.Equal(a, b)
}

func deriveMaintenanceKey(ctx context.Context, deriver ports.KeyDeriver) ([]byte, error) {
	if deriver == nil {
		return nil, ErrReceiptInvalid
	}
	key, err := deriver.DeriveKey(ctx, ports.KeyPurposeBackupManifest, ports.KeyDerivationVersion)
	if err != nil || len(key) == 0 {
		return nil, ErrReceiptInvalid
	}
	return key, nil
}

func isRegularMode(path string, mode fs.FileMode) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != mode.Perm() || !owns(info) {
		return nil, ErrUnsafePath
	}
	return info, nil
}

func safeJoin(dir, name string) (string, error) {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00\r\n") {
		return "", ErrUnsafePath
	}
	path := filepath.Join(dir, name)
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", ErrUnsafePath
	}
	return path, nil
}
