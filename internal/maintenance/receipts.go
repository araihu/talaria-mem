package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/filesystem"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type ReceiptKind string

const (
	ReceiptPurge           ReceiptKind = "memory_purge"
	ReceiptBackupReconcile ReceiptKind = "backup_reconcile"
	ReceiptRestore         ReceiptKind = "database_restore"
	ReceiptRuleUpgrade     ReceiptKind = "rule_upgrade"
)

func (kind ReceiptKind) valid() bool {
	switch kind {
	case ReceiptPurge, ReceiptBackupReconcile, ReceiptRestore, ReceiptRuleUpgrade:
		return true
	default:
		return false
	}
}

type ReceiptStatus string

const (
	ReceiptUnused   ReceiptStatus = "unused"
	ReceiptClaimed  ReceiptStatus = "claimed"
	ReceiptComplete ReceiptStatus = "complete"
)

// Receipt is deliberately metadata-only.  In particular, it contains no
// title, body, tags, scanner match, or serialized database bytes.  The fields
// are shared by maintenance operations so receipt verification remains one
// bounded protocol instead of four subtly different authorization paths.
type Receipt struct {
	Version               int                   `json:"version"`
	ID                    string                `json:"id"`
	Kind                  ReceiptKind           `json:"kind"`
	Status                ReceiptStatus         `json:"status"`
	CreatedAt             time.Time             `json:"created_at"`
	ExpiresAt             time.Time             `json:"expires_at"`
	WorkspaceID           string                `json:"workspace_id,omitempty"`
	MemoryID              string                `json:"memory_id,omitempty"`
	ExpectedRevisionID    string                `json:"expected_revision_id,omitempty"`
	ProjectionFingerprint string                `json:"projection_fingerprint,omitempty"`
	ProjectionIDs         []string              `json:"projection_ids,omitempty"`
	InventoryWatermark    int64                 `json:"inventory_watermark,omitempty"`
	BackupIDs             []string              `json:"backup_ids,omitempty"`
	BackupID              string                `json:"backup_id,omitempty"`
	Path                  string                `json:"path,omitempty"`
	OwnerID               string                `json:"owner_id,omitempty"`
	Mode                  uint32                `json:"mode,omitempty"`
	Fingerprint           ports.FileFingerprint `json:"fingerprint,omitempty"`
	Action                string                `json:"action,omitempty"`
	Scope                 string                `json:"scope,omitempty"`
	ActiveGeneration      string                `json:"active_generation,omitempty"`
	CandidateGeneration   string                `json:"candidate_generation,omitempty"`
	CandidateFingerprint  string                `json:"candidate_fingerprint,omitempty"`
	RevisionWatermark     int64                 `json:"revision_watermark,omitempty"`
	Claim                 string                `json:"claim,omitempty"`
	ConsumedAt            *time.Time            `json:"consumed_at,omitempty"`
	MAC                   string                `json:"mac"`
}

func (receipt Receipt) unsigned() Receipt {
	receipt.MAC = ""
	return receipt
}

func (receipt Receipt) validate() error {
	if receipt.Version != 1 || !receipt.Kind.valid() || receipt.ID == "" || receipt.Status == "" {
		return ErrReceiptInvalid
	}
	if receipt.Status != ReceiptUnused && receipt.Status != ReceiptClaimed && receipt.Status != ReceiptComplete {
		return ErrReceiptInvalid
	}
	if receipt.CreatedAt.IsZero() || receipt.ExpiresAt.IsZero() || !receipt.ExpiresAt.After(receipt.CreatedAt) {
		return ErrReceiptInvalid
	}
	if receipt.InventoryWatermark < 0 || receipt.RevisionWatermark < 0 || receipt.Fingerprint.Size < 0 {
		return ErrReceiptInvalid
	}
	for _, field := range []struct {
		value string
		max   int
		req   bool
	}{
		{receipt.ID, 128, true}, {receipt.WorkspaceID, 256, false},
		{receipt.MemoryID, 256, false}, {receipt.ExpectedRevisionID, 256, false},
		{receipt.ProjectionFingerprint, 128, false}, {receipt.BackupID, 256, false},
		{receipt.Path, 4096, false}, {receipt.OwnerID, 256, false},
		{receipt.Action, 128, false}, {receipt.Scope, 256, false},
		{receipt.ActiveGeneration, 128, false}, {receipt.CandidateGeneration, 128, false},
		{receipt.CandidateFingerprint, 128, false}, {receipt.Claim, 128, false},
	} {
		if err := validateMetadata(field.value, field.max, field.req); err != nil {
			return err
		}
	}
	if receipt.Fingerprint.Mode != 0 && receipt.Fingerprint.Mode.Perm() != ManagedFileMode {
		return ErrReceiptInvalid
	}
	for _, id := range receipt.BackupIDs {
		if err := validateMetadata(id, 256, true); err != nil {
			return err
		}
	}
	for _, id := range receipt.ProjectionIDs {
		if err := validateMetadata(id, 256, true); err != nil {
			return err
		}
	}
	return nil
}

type ReceiptStore struct {
	Dir   string
	Key   []byte
	Clock ports.Clock
	TTL   time.Duration
}

func NewReceiptStore(stateDir string, key []byte) (*ReceiptStore, error) {
	dir, err := ensureReceiptDirectory(stateDir)
	if err != nil {
		return nil, err
	}
	if len(key) < 16 {
		return nil, ErrReceiptInvalid
	}
	return &ReceiptStore{Dir: dir, Key: append([]byte(nil), key...), TTL: ReceiptTTL}, nil
}

func NewReceiptStoreAt(receiptDir string, key []byte) (*ReceiptStore, error) {
	return NewReceiptStore(filepath.Clean(receiptDir), key)
}

func NewReceiptStoreFromDeriver(ctx context.Context, stateDir string, deriver ports.KeyDeriver) (*ReceiptStore, error) {
	key, err := deriveMaintenanceKey(ctx, deriver)
	if err != nil {
		return nil, err
	}
	return NewReceiptStore(stateDir, key)
}

func (store *ReceiptStore) path(id string) (string, error) {
	if store == nil {
		return "", ErrReceiptInvalid
	}
	if err := validateManagedDirectory(store.Dir); err != nil {
		return "", err
	}
	if err := validateMetadata(id, 128, true); err != nil {
		return "", err
	}
	return safeJoin(store.Dir, id+".json")
}

func (store *ReceiptStore) digest(receipt Receipt) (string, error) {
	if len(store.Key) < 16 {
		return "", ErrReceiptInvalid
	}
	bytes, err := json.Marshal(receipt.unsigned())
	if err != nil {
		return "", ErrReceiptInvalid
	}
	return hmacHex(store.Key, bytes), nil
}

func (store *ReceiptStore) authenticate(receipt Receipt) error {
	if err := receipt.validate(); err != nil {
		return err
	}
	digest, err := store.digest(receipt)
	if err != nil || !secureEqualHex(digest, receipt.MAC) {
		return ErrReceiptInvalid
	}
	return nil
}

func (store *ReceiptStore) Create(ctx context.Context, receipt Receipt) (Receipt, string, error) {
	if err := contextErr(ctx); err != nil {
		return Receipt{}, "", err
	}
	if receipt.ID == "" {
		receipt.ID = operationID()
	}
	now := clockNow(store.Clock)
	if receipt.Version == 0 {
		receipt.Version = 1
	}
	if receipt.Status == "" {
		receipt.Status = ReceiptUnused
	}
	if receipt.Status != ReceiptUnused {
		return Receipt{}, "", ErrReceiptInvalid
	}
	if receipt.CreatedAt.IsZero() {
		receipt.CreatedAt = now
	}
	if receipt.ExpiresAt.IsZero() {
		ttl := store.TTL
		if ttl <= 0 {
			ttl = ReceiptTTL
		}
		receipt.ExpiresAt = receipt.CreatedAt.Add(ttl)
	}
	if receipt.Fingerprint.Mode == 0 && receipt.Path != "" {
		receipt.Fingerprint.Mode = ManagedFileMode
	}
	if err := receipt.validate(); err != nil {
		return Receipt{}, "", err
	}
	path, err := store.path(receipt.ID)
	if err != nil {
		return Receipt{}, "", err
	}
	digest, err := store.digest(receipt)
	if err != nil {
		return Receipt{}, "", err
	}
	receipt.MAC = digest
	if _, err := os.Lstat(path); err == nil {
		return Receipt{}, "", ErrReceiptDrift
	} else if !errors.Is(err, os.ErrNotExist) {
		return Receipt{}, "", err
	}
	if _, err := writeManaged(ctx, path, receipt, nil); err != nil {
		return Receipt{}, "", err
	}
	return receipt, path, nil
}

func (store *ReceiptStore) Load(ctx context.Context, id string) (Receipt, string, error) {
	if err := contextErr(ctx); err != nil {
		return Receipt{}, "", err
	}
	path, err := store.path(id)
	if err != nil {
		return Receipt{}, "", err
	}
	var receipt Receipt
	if _, err := readManaged(path, &receipt); err != nil {
		return Receipt{}, path, err
	}
	if err := store.authenticate(receipt); err != nil {
		return Receipt{}, path, err
	}
	if receipt.Status == ReceiptUnused && !clockNow(store.Clock).Before(receipt.ExpiresAt) {
		return Receipt{}, path, ErrReceiptExpired
	}
	return receipt, path, nil
}

// Claim performs the receipt's compare-and-swap from unused to claimed.  The
// caller must perform path/owner/mode/fingerprint validation in check before
// calling Apply; claim itself only changes the receipt after authentication.
func (store *ReceiptStore) Claim(ctx context.Context, id string, check func(Receipt) error) (Receipt, string, error) {
	receipt, path, err := store.Load(ctx, id)
	if err != nil {
		return Receipt{}, path, err
	}
	if receipt.Status != ReceiptUnused {
		return Receipt{}, path, ErrReceiptUsed
	}
	if check != nil {
		if err := check(receipt); err != nil {
			return Receipt{}, path, err
		}
	}
	old, err := fingerprint(path)
	if err != nil {
		return Receipt{}, path, err
	}
	receipt.Status = ReceiptClaimed
	receipt.Claim = operationID()
	digest, err := store.digest(receipt)
	if err != nil {
		return Receipt{}, path, err
	}
	receipt.MAC = digest
	if _, err := writeManaged(ctx, path, receipt, &old); err != nil {
		if errors.Is(err, filesystem.ErrFingerprintMismatch) {
			return Receipt{}, path, ErrReceiptDrift
		}
		return Receipt{}, path, err
	}
	return receipt, path, nil
}

func (store *ReceiptStore) Complete(ctx context.Context, receipt Receipt, path string) (Receipt, error) {
	if receipt.Status != ReceiptClaimed || receipt.Claim == "" {
		return Receipt{}, ErrReceiptInvalid
	}
	old, err := fingerprint(path)
	if err != nil {
		return Receipt{}, err
	}
	now := clockNow(store.Clock)
	receipt.Status = ReceiptComplete
	receipt.ConsumedAt = &now
	digest, err := store.digest(receipt)
	if err != nil {
		return Receipt{}, err
	}
	receipt.MAC = digest
	if _, err := writeManaged(ctx, path, receipt, &old); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}
