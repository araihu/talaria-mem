package lifecycle

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/adapters/filesystem"
)

const ftsRepairReceiptTTL = 30 * time.Minute

// FileFTSRepairReceiptStore persists only bounded FTS metadata and hashes. It
// shares the managed state/receipts directory with other maintenance receipts
// while using a distinct MAC namespace for the repair operation.
type FileFTSRepairReceiptStore struct {
	Dir   string
	Key   []byte
	Clock func() time.Time
}

type ftsRepairReceipt struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Report    FTSReport `json:"report"`
	MAC       string    `json:"mac"`
}

func NewFileFTSRepairReceiptStore(dir string, key []byte) (*FileFTSRepairReceiptStore, error) {
	if err := validateManagedDirectory(dir); err != nil {
		return nil, err
	}
	if len(key) < 16 {
		return nil, errors.New("FTS receipt key is unavailable")
	}
	return &FileFTSRepairReceiptStore{Dir: dir, Key: append([]byte(nil), key...)}, nil
}

func (store *FileFTSRepairReceiptStore) Create(ctx context.Context, report FTSReport) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if store == nil || len(store.Key) < 16 {
		return "", errors.New("FTS repair receipt unavailable")
	}
	now := time.Now().UTC()
	if store.Clock != nil {
		now = store.Clock().UTC()
	}
	receipt := ftsRepairReceipt{Version: 1, ID: uuid.NewString(), CreatedAt: now, ExpiresAt: now.Add(ftsRepairReceiptTTL), Report: report}
	receipt.MAC = store.mac(receipt)
	path := filepath.Join(store.Dir, receipt.ID+".json")
	if filepath.Dir(path) != filepath.Clean(store.Dir) || strings.ContainsAny(receipt.ID, `/\\`) {
		return "", errors.New("FTS repair receipt path is unsafe")
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	if err := filesystem.AtomicWriteNoFollow(ctx, path, data); err != nil {
		return "", err
	}
	return path, nil
}

func (store *FileFTSRepairReceiptStore) Consume(ctx context.Context, path string, report FTSReport) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if store == nil || len(store.Key) < 16 {
		return errors.New("FTS repair receipt unavailable")
	}
	if path == "" || filepath.Dir(filepath.Clean(path)) != filepath.Clean(store.Dir) {
		return errors.New("FTS repair receipt path is unsafe")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != ManagedFileMode.Perm() || !currentUserOwns(info) {
		return errors.New("FTS repair receipt is unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var receipt ftsRepairReceipt
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.Version != 1 || receipt.ID == "" || receipt.ExpiresAt.IsZero() || !receipt.ExpiresAt.After(receipt.CreatedAt) {
		return errors.New("FTS repair receipt is invalid")
	}
	now := time.Now().UTC()
	if store.Clock != nil {
		now = store.Clock().UTC()
	}
	if !now.Before(receipt.ExpiresAt) || !hmac.Equal([]byte(receipt.MAC), []byte(store.mac(receipt))) || receipt.Report != report {
		return errors.New("FTS repair receipt is invalid or stale")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

func (store *FileFTSRepairReceiptStore) mac(receipt ftsRepairReceipt) string {
	unsigned := receipt
	unsigned.MAC = ""
	data, _ := json.Marshal(unsigned)
	mac := hmac.New(sha256.New, store.Key)
	_, _ = mac.Write([]byte("talaria-mem/fts-repair/v1\x00"))
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

var _ FTSRepairReceiptStore = (*FileFTSRepairReceiptStore)(nil)
