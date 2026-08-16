package maintenance

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// RestoreBackend owns the concrete SQLite close/replace/reopen boundary.  It
// exposes only safe metadata and scanner fields to this orchestration layer.
type RestoreBackend interface {
	Verify(ctx context.Context, backupPath string) error
	Fields(ctx context.Context, backupPath string) ([]ports.TextField, error)
	Install(ctx context.Context, backupPath string) error
	RebuildInventory(ctx context.Context) error
}

type RestoreManager struct {
	Inventory    *InventoryReader
	ReceiptStore *ReceiptStore
	Backend      RestoreBackend
	Scanner      ports.Scanner
	LockPath     string
	Clock        ports.Clock
	Failpoint    func(string) error
}

func NewRestoreManager(inventory *InventoryReader, receipts *ReceiptStore, backend RestoreBackend, scanner ports.Scanner, lockPath string, clock ports.Clock) *RestoreManager {
	return &RestoreManager{Inventory: inventory, ReceiptStore: receipts, Backend: backend, Scanner: scanner, LockPath: lockPath, Clock: clock}
}

func (manager *RestoreManager) DryRun(ctx context.Context, backupID string) (Receipt, string, error) {
	if manager == nil || manager.Inventory == nil || manager.ReceiptStore == nil {
		return Receipt{}, "", ErrReceiptInvalid
	}
	if err := validateMetadata(backupID, 128, true); err != nil {
		return Receipt{}, "", err
	}
	inventory, err := manager.Inventory.Read(ctx)
	if err != nil {
		return Receipt{}, "", err
	}
	if !inventory.Ready {
		return Receipt{}, "", ErrInventoryUnready
	}
	var selected *BackupManifest
	for index := range inventory.Entries {
		if inventory.Entries[index].ID == backupID {
			selected = &inventory.Entries[index]
			break
		}
	}
	if selected == nil || selected.Lifecycle != BackupComplete {
		return Receipt{}, "", domain.NewError(domain.CodeNotFound, "backup not found", false)
	}
	fp, err := fingerprint(selected.Path)
	if err != nil {
		return Receipt{}, "", err
	}
	receipt := Receipt{Kind: ReceiptRestore, BackupID: selected.ID, Path: selected.Path, Fingerprint: fp, Action: "restore", Scope: "database", RevisionWatermark: selected.DatabaseRevisionWatermark}
	return manager.ReceiptStore.Create(ctx, receipt)
}

func (manager *RestoreManager) Apply(ctx context.Context, receiptID string) error {
	if manager == nil || manager.Inventory == nil || manager.ReceiptStore == nil || manager.Backend == nil {
		return ErrReceiptInvalid
	}
	if manager.Scanner == nil {
		return ErrScannerUnavailable
	}
	lockPath := manager.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(manager.Inventory.Dir), ".maintenance.lock")
	}
	return WithLock(ctx, lockPath, func(ctx context.Context) error {
		// Scanner-gate before claim.  A scanner outage or uncertain result must
		// leave the receipt unused and the live database byte-for-byte untouched.
		receipt, _, err := manager.ReceiptStore.Load(ctx, receiptID)
		if err != nil {
			return err
		}
		if receipt.Kind != ReceiptRestore || receipt.Action != "restore" || receipt.Path == "" {
			return ErrReceiptInvalid
		}
		fields, err := manager.Backend.Fields(ctx, receipt.Path)
		if err != nil {
			return err
		}
		result := manager.Scanner.Scan(ctx, fields)
		if result.Status == ports.ScanFinding {
			return domain.NewError(domain.CodeSecretRefusal, "restore content refused", false)
		}
		if result.Status != ports.ScanClean {
			return ErrScannerUnavailable
		}
		if err := manager.Backend.Verify(ctx, receipt.Path); err != nil {
			return errors.Join(ErrIntegrityFailed, err)
		}
		claimed, path, err := manager.ReceiptStore.Claim(ctx, receiptID, func(receipt Receipt) error {
			actual, fingerprintErr := fingerprint(receipt.Path)
			if fingerprintErr != nil || !sameFingerprint(actual, receipt.Fingerprint) {
				return ErrReceiptDrift
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := manager.fail("before_install"); err != nil {
			return err
		}
		if err := manager.Backend.Install(ctx, claimed.Path); err != nil {
			return err
		}
		if err := manager.fail("after_install"); err != nil {
			return err
		}
		if err := manager.Backend.RebuildInventory(ctx); err != nil {
			return err
		}
		if err := manager.fail("after_inventory"); err != nil {
			return err
		}
		_, err = manager.ReceiptStore.Complete(ctx, claimed, path)
		return err
	})
}

func (manager *RestoreManager) fail(phase string) error {
	if manager.Failpoint == nil {
		return nil
	}
	return manager.Failpoint(phase)
}

type ScannerRestoreBackend struct {
	FieldsFunc    func(context.Context, string) ([]ports.TextField, error)
	VerifyFunc    func(context.Context, string) error
	InstallFunc   func(context.Context, string) error
	InventoryFunc func(context.Context) error
}

func (backend ScannerRestoreBackend) Fields(ctx context.Context, path string) ([]ports.TextField, error) {
	if backend.FieldsFunc == nil {
		return nil, ErrReceiptInvalid
	}
	return backend.FieldsFunc(ctx, path)
}
func (backend ScannerRestoreBackend) Verify(ctx context.Context, path string) error {
	if backend.VerifyFunc == nil {
		return nil
	}
	return backend.VerifyFunc(ctx, path)
}
func (backend ScannerRestoreBackend) Install(ctx context.Context, path string) error {
	if backend.InstallFunc == nil {
		return ErrReceiptInvalid
	}
	return backend.InstallFunc(ctx, path)
}
func (backend ScannerRestoreBackend) RebuildInventory(ctx context.Context) error {
	if backend.InventoryFunc == nil {
		return nil
	}
	return backend.InventoryFunc(ctx)
}
