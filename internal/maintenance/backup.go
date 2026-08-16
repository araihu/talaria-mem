package maintenance

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// DatabaseSnapshotter is the maintenance-only database boundary.  The
// concrete SQLite adapter may implement Snapshot with VACUUM INTO; the
// maintenance protocol never reaches into a normal repository transaction.
type DatabaseSnapshotter interface {
	Snapshot(ctx context.Context, destination string) error
	Integrity(ctx context.Context, databasePath string) error
	SchemaVersion(ctx context.Context) (int64, error)
	RevisionWatermark(ctx context.Context) (int64, error)
}

type FileDatabaseSnapshotter struct {
	Source string
}

func (snapshotter FileDatabaseSnapshotter) Snapshot(ctx context.Context, destination string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if _, err := validateManagedFile(snapshotter.Source); err != nil {
		return err
	}
	if _, err := validateManagedFile(destination); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	source, err := os.Open(snapshotter.Source)
	if err != nil {
		return err
	}
	defer source.Close()
	destinationFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, ManagedFileMode.Perm())
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = destinationFile.Close()
		if remove {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(destinationFile, source); err != nil {
		return err
	}
	if err := destinationFile.Sync(); err != nil {
		return err
	}
	if err := destinationFile.Close(); err != nil {
		return err
	}
	remove = false
	return syncDirectory(destination)
}

func (snapshotter FileDatabaseSnapshotter) Integrity(ctx context.Context, databasePath string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	_, err := validateManagedFile(databasePath)
	return err
}

func (snapshotter FileDatabaseSnapshotter) SchemaVersion(context.Context) (int64, error) {
	return BackupSchemaVersion, nil
}

func (snapshotter FileDatabaseSnapshotter) RevisionWatermark(context.Context) (int64, error) {
	return 0, nil
}

type BackupManager struct {
	Dir          string
	Key          []byte
	OwnerID      string
	KeyVersion   uint32
	Clock        ports.Clock
	ReceiptStore *ReceiptStore
	LockPath     string
	Failpoint    func(string) error
	mu           sync.Mutex
	operations   map[string]*BackupReconcileOperation
}

func NewBackupManager(dir string, key []byte, clock ports.Clock) (*BackupManager, error) {
	if err := validateManagedDirectory(dir); err != nil {
		return nil, err
	}
	if len(key) < 16 {
		return nil, ErrReceiptInvalid
	}
	return &BackupManager{
		Dir: dir, Key: append([]byte(nil), key...), OwnerID: ownerID(), KeyVersion: 1,
		Clock: clock, operations: make(map[string]*BackupReconcileOperation),
	}, nil
}

func (manager *BackupManager) SetReceiptStore(store *ReceiptStore) *BackupManager {
	manager.ReceiptStore = store
	return manager
}

func (manager *BackupManager) SetLockPath(path string) *BackupManager {
	manager.LockPath = path
	return manager
}

type BackupRequest struct {
	CreationCause             string
	DatabaseRevisionWatermark int64
	SchemaVersion             int64
}

func (manager *BackupManager) Create(ctx context.Context, request BackupRequest, source DatabaseSnapshotter) (BackupManifest, error) {
	if manager == nil || source == nil {
		return BackupManifest{}, ErrReceiptInvalid
	}
	if err := contextErr(ctx); err != nil {
		return BackupManifest{}, err
	}
	if err := validateMetadata(request.CreationCause, 128, true); err != nil {
		return BackupManifest{}, err
	}
	if request.DatabaseRevisionWatermark < 0 || request.SchemaVersion < 0 {
		return BackupManifest{}, ErrReceiptInvalid
	}
	if request.SchemaVersion == 0 {
		version, err := source.SchemaVersion(ctx)
		if err != nil {
			return BackupManifest{}, err
		}
		request.SchemaVersion = version
	}
	if request.DatabaseRevisionWatermark == 0 {
		watermark, err := source.RevisionWatermark(ctx)
		if err != nil {
			return BackupManifest{}, err
		}
		request.DatabaseRevisionWatermark = watermark
	}
	id := uuid.NewString()
	partial, err := safeJoin(manager.Dir, id+".partial"+BackupFileSuffix)
	if err != nil {
		return BackupManifest{}, err
	}
	path, err := safeJoin(manager.Dir, id+BackupFileSuffix)
	if err != nil {
		return BackupManifest{}, err
	}
	manifestPath, err := safeJoin(manager.Dir, id+BackupManifestSuffix)
	if err != nil {
		return BackupManifest{}, err
	}
	now := clockNow(manager.Clock).Format(time.RFC3339Nano)
	manifest := BackupManifest{
		Version: 1, ID: id, Path: path, Mode: uint32(ManagedFileMode.Perm()), CreationCause: request.CreationCause,
		SchemaVersion: request.SchemaVersion, DatabaseRevisionWatermark: request.DatabaseRevisionWatermark,
		Lifecycle: BackupPending, OwnerID: manager.OwnerID, KeyVersion: manager.KeyVersion,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := manager.authenticateManifest(&manifest); err != nil {
		return BackupManifest{}, err
	}
	if _, err := writeManaged(ctx, manifestPath, manifest, nil); err != nil {
		return BackupManifest{}, err
	}
	if err := manager.fail("backup_manifest_pending"); err != nil {
		return BackupManifest{}, err
	}
	if err := source.Snapshot(ctx, partial); err != nil {
		return BackupManifest{}, err
	}
	if err := os.Chmod(partial, ManagedFileMode.Perm()); err != nil {
		return BackupManifest{}, err
	}
	if err := manager.fail("backup_snapshot"); err != nil {
		return BackupManifest{}, err
	}
	if _, err := validateManagedFile(partial); err != nil {
		return BackupManifest{}, err
	}
	actual, err := fingerprint(partial)
	if err != nil {
		return BackupManifest{}, err
	}
	manifest.SHA256 = actual.SHA256Hex
	manifest.Lifecycle = BackupComplete
	manifest.UpdatedAt = clockNow(manager.Clock).Format(time.RFC3339Nano)
	if err := source.Integrity(ctx, partial); err != nil {
		return BackupManifest{}, errors.Join(ErrIntegrityFailed, err)
	}
	if err := manager.fail("backup_integrity"); err != nil {
		return BackupManifest{}, err
	}
	if err := os.Rename(partial, path); err != nil {
		return BackupManifest{}, err
	}
	if err := syncDirectory(path); err != nil {
		return BackupManifest{}, err
	}
	if err := manager.authenticateManifest(&manifest); err != nil {
		return BackupManifest{}, err
	}
	old, err := fingerprint(manifestPath)
	if err != nil {
		return BackupManifest{}, err
	}
	if _, err := writeManaged(ctx, manifestPath, manifest, &old); err != nil {
		return BackupManifest{}, err
	}
	if err := manager.fail("backup_manifest_complete"); err != nil {
		return BackupManifest{}, err
	}
	return manifest, nil
}

func (manager *BackupManager) authenticateManifest(manifest *BackupManifest) error {
	if manifest == nil {
		return ErrReceiptInvalid
	}
	digest, err := jsonDigest(manager.Key, manifest.unsigned())
	if err != nil {
		return err
	}
	manifest.MAC = digest
	return manifest.validate()
}

func (manager *BackupManager) fail(phase string) error {
	if manager.Failpoint == nil {
		return nil
	}
	return manager.Failpoint(phase)
}

func (manager *BackupManager) Inventory(ctx context.Context) (Inventory, error) {
	reader, err := NewInventoryReader(manager.Dir, manager.Key)
	if err != nil {
		return Inventory{}, err
	}
	reader.OwnerID, reader.KeyVersion = manager.OwnerID, manager.KeyVersion
	return reader.Read(ctx)
}

// BackupReconcileOperation records only non-content recovery state.  It is
// intentionally kept outside the receipt: a claimed receipt is not reusable,
// while this operation is resumable after a process crash.
type BackupReconcilePhase string

const (
	ReconcilePreEffect         BackupReconcilePhase = "pre_effect"
	ReconcileQuarantineRenamed BackupReconcilePhase = "quarantine_renamed"
	ReconcileFileFsynced       BackupReconcilePhase = "file_fsynced"
	ReconcileDeleted           BackupReconcilePhase = "deleted"
	ReconcileInventoryRebuilt  BackupReconcilePhase = "inventory_rebuilt"
	ReconcileReceiptComplete   BackupReconcilePhase = "receipt_complete"
	ReconcileFailed            BackupReconcilePhase = "failed"
)

type BackupReconcileOperation struct {
	ID        string               `json:"id"`
	ReceiptID string               `json:"receipt_id"`
	Path      string               `json:"path"`
	Action    string               `json:"action"`
	Phase     BackupReconcilePhase `json:"phase"`
	SafeError string               `json:"safe_error,omitempty"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type ReconcilePlan struct {
	Unknown InventoryUnknown `json:"unknown"`
	Action  string           `json:"action"`
}

func (manager *BackupManager) ReconcileDryRun(ctx context.Context) (Receipt, string, error) {
	if manager == nil || manager.ReceiptStore == nil {
		return Receipt{}, "", ErrReceiptInvalid
	}
	inventory, err := manager.Inventory(ctx)
	if err != nil {
		return Receipt{}, "", err
	}
	if len(inventory.Unknown) == 0 {
		return Receipt{}, "", ErrOperationAlreadyDone
	}
	unknown := inventory.Unknown[0]
	receipt := Receipt{Kind: ReceiptBackupReconcile, Path: unknown.Path, Fingerprint: unknown.Fingerprint, Action: unknown.Action, Scope: "backup-inventory"}
	return manager.ReceiptStore.Create(ctx, receipt)
}

func (manager *BackupManager) ReconcileApply(ctx context.Context, receiptID string) error {
	if manager == nil || manager.ReceiptStore == nil {
		return ErrReceiptInvalid
	}
	lockPath := manager.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(manager.Dir), ".maintenance.lock")
	}
	return WithLock(ctx, lockPath, func(ctx context.Context) error {
		receipt, path, err := manager.ReceiptStore.Claim(ctx, receiptID, func(receipt Receipt) error {
			if receipt.Kind != ReceiptBackupReconcile || receipt.Path == "" || receipt.Action == "" {
				return ErrReceiptInvalid
			}
			actual, fingerprintErr := fingerprint(receipt.Path)
			if fingerprintErr != nil || !sameFingerprint(actual, receipt.Fingerprint) {
				return ErrReceiptDrift
			}
			return nil
		})
		if err != nil {
			return err
		}
		operation := &BackupReconcileOperation{ID: operationID(), ReceiptID: receipt.ID, Path: receipt.Path, Action: receipt.Action, Phase: ReconcilePreEffect, UpdatedAt: clockNow(manager.Clock)}
		manager.mu.Lock()
		manager.operations[operation.ID] = operation
		manager.mu.Unlock()
		if err := manager.applyOperation(ctx, operation); err != nil {
			return err
		}
		_, err = manager.ReceiptStore.Complete(ctx, receipt, path)
		return err
	})
}

func (manager *BackupManager) applyOperation(ctx context.Context, operation *BackupReconcileOperation) error {
	if operation == nil {
		return ErrOperationIncomplete
	}
	if err := manager.fail(string(operation.Phase)); err != nil {
		operation.Phase = ReconcileFailed
		operation.SafeError = SafeError(err)
		return err
	}
	if operation.Phase == ReconcilePreEffect {
		if operation.Action == "quarantine" {
			quarantine := operation.Path + ".quarantine-" + operation.ID
			if _, err := os.Lstat(quarantine); errors.Is(err, os.ErrNotExist) {
				if err := os.Rename(operation.Path, quarantine); err != nil {
					return err
				}
			}
			operation.Phase = ReconcileQuarantineRenamed
		} else if operation.Action == "delete" {
			if err := os.Remove(operation.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			operation.Phase = ReconcileDeleted
		} else {
			return ErrReceiptInvalid
		}
		if err := manager.fail(string(operation.Phase)); err != nil {
			operation.SafeError = SafeError(err)
			return err
		}
	}
	if operation.Phase == ReconcileQuarantineRenamed || operation.Phase == ReconcileDeleted {
		if err := syncDirectory(operation.Path); err != nil {
			return err
		}
		operation.Phase = ReconcileFileFsynced
		if err := manager.fail(string(operation.Phase)); err != nil {
			operation.SafeError = SafeError(err)
			return err
		}
	}
	if operation.Phase == ReconcileFileFsynced {
		if _, err := manager.Inventory(ctx); err != nil {
			// A malformed or quarantined item intentionally keeps readiness
			// false, but inventory reconstruction itself still completes.
			if !errors.Is(err, ErrInventoryUnready) {
				return err
			}
		}
		operation.Phase = ReconcileInventoryRebuilt
		if err := manager.fail(string(operation.Phase)); err != nil {
			operation.SafeError = SafeError(err)
			return err
		}
	}
	if operation.Phase == ReconcileInventoryRebuilt {
		operation.Phase = ReconcileReceiptComplete
	}
	return nil
}

func (manager *BackupManager) ResumeReconcile(ctx context.Context, operationID string) error {
	manager.mu.Lock()
	operation := manager.operations[operationID]
	manager.mu.Unlock()
	if operation == nil {
		return ErrOperationIncomplete
	}
	lockPath := manager.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(manager.Dir), ".maintenance.lock")
	}
	return WithLock(ctx, lockPath, func(ctx context.Context) error {
		if operation.Phase == ReconcileReceiptComplete {
			receipt, path, err := manager.ReceiptStore.Load(ctx, operation.ReceiptID)
			if err != nil {
				return err
			}
			if receipt.Status == ReceiptClaimed {
				_, err = manager.ReceiptStore.Complete(ctx, receipt, path)
			}
			return err
		}
		if err := manager.applyOperation(ctx, operation); err != nil {
			return err
		}
		receipt, path, err := manager.ReceiptStore.Load(ctx, operation.ReceiptID)
		if err != nil {
			return err
		}
		if receipt.Status == ReceiptClaimed {
			_, err = manager.ReceiptStore.Complete(ctx, receipt, path)
		}
		return err
	})
}

func (manager *BackupManager) OperationIDForReceipt(receiptID string) string {
	if manager == nil {
		return ""
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for id, operation := range manager.operations {
		if operation != nil && operation.ReceiptID == receiptID {
			return id
		}
	}
	return ""
}

func (manager *BackupManager) BackupPath(id string) (string, error) {
	if err := validateMetadata(id, 128, true); err != nil {
		return "", err
	}
	return safeJoin(manager.Dir, id+BackupFileSuffix)
}

func (manager *BackupManager) ManifestPath(id string) (string, error) {
	if err := validateMetadata(id, 128, true); err != nil {
		return "", err
	}
	return safeJoin(manager.Dir, id+BackupManifestSuffix)
}
