package maintenance

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type PurgePhase string

const (
	PurgeReserved      PurgePhase = "reserved"
	PurgePending       PurgePhase = "purge_pending"
	PurgePreEffect     PurgePhase = "pre_effect"
	PurgeEffectApplied PurgePhase = "effect_applied"
	PurgePostEffect    PurgePhase = "post_effect"
	PurgeComplete      PurgePhase = "complete"
	PurgeFailed        PurgePhase = "failed"
)

type PurgeTarget struct {
	WorkspaceID        string
	MemoryID           string
	ExpectedRevisionID string
	ProjectionIDs      []string
	BackupIDs          []string
	InventoryWatermark int64
}

func (target PurgeTarget) validate() error {
	for _, field := range []struct {
		value string
		max   int
		req   bool
	}{
		{target.WorkspaceID, 256, false}, {target.MemoryID, 256, true}, {target.ExpectedRevisionID, 256, true},
	} {
		if err := validateMetadata(field.value, field.max, field.req); err != nil {
			return err
		}
	}
	if target.InventoryWatermark < 0 {
		return ErrReceiptInvalid
	}
	for _, value := range append(append([]string{}, target.ProjectionIDs...), target.BackupIDs...) {
		if err := validateMetadata(value, 256, true); err != nil {
			return err
		}
	}
	return nil
}

// PurgeEffects is the narrow mutation boundary.  Implementations normally
// wrap one SQLite transaction plus the projection/file adapter.  The manager
// never accepts content and therefore remains scanner-independent.
type PurgeEffects interface {
	Validate(ctx context.Context, target PurgeTarget) error
	MarkPending(ctx context.Context, target PurgeTarget, operationID string) error
	RemoveCanonical(ctx context.Context, target PurgeTarget) error
	CheckpointWAL(ctx context.Context) error
	RebuildProjections(ctx context.Context, target PurgeTarget) error
	DeleteBackups(ctx context.Context, target PurgeTarget) error
	MarkComplete(ctx context.Context, target PurgeTarget, operationID string) error
}

type PurgeOperation struct {
	ID          string      `json:"id"`
	ReceiptID   string      `json:"receipt_id"`
	Target      PurgeTarget `json:"target"`
	Phase       PurgePhase  `json:"phase"`
	ResumePhase PurgePhase  `json:"resume_phase,omitempty"`
	SafeError   string      `json:"safe_error,omitempty"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type DeletionReceipt struct {
	ID          string    `json:"id"`
	OperationID string    `json:"operation_id"`
	MemoryID    string    `json:"memory_id"`
	CompletedAt time.Time `json:"completed_at"`
}

type PurgeResult struct {
	Operation       PurgeOperation  `json:"operation"`
	DeletionReceipt DeletionReceipt `json:"deletion_receipt"`
}

type PurgeManager struct {
	Effects      PurgeEffects
	ReceiptStore *ReceiptStore
	LockPath     string
	Clock        ports.Clock
	Failpoint    func(PurgePhase) error
	mu           sync.Mutex
	operations   map[string]*PurgeOperation
	completed    map[string]PurgeResult
}

func NewPurgeManager(effects PurgeEffects, receipts *ReceiptStore, lockPath string, clock ports.Clock) *PurgeManager {
	return &PurgeManager{Effects: effects, ReceiptStore: receipts, LockPath: lockPath, Clock: clock, operations: make(map[string]*PurgeOperation), completed: make(map[string]PurgeResult)}
}

func (manager *PurgeManager) DryRun(ctx context.Context, target PurgeTarget) (Receipt, string, error) {
	if manager == nil || manager.ReceiptStore == nil {
		return Receipt{}, "", ErrReceiptInvalid
	}
	if err := target.validate(); err != nil {
		return Receipt{}, "", err
	}
	receipt := Receipt{Kind: ReceiptPurge, WorkspaceID: target.WorkspaceID, MemoryID: target.MemoryID, ExpectedRevisionID: target.ExpectedRevisionID, ProjectionFingerprint: hashIDs(target.ProjectionIDs), ProjectionIDs: append([]string(nil), target.ProjectionIDs...), InventoryWatermark: target.InventoryWatermark, BackupIDs: append([]string(nil), target.BackupIDs...)}
	return manager.ReceiptStore.Create(ctx, receipt)
}

func hashIDs(values []string) string {
	// The digest is an opaque receipt binding; it does not contain query or
	// memory content.  Sorting makes the dry-run inventory deterministic.
	copyValues := append([]string(nil), values...)
	for i := 1; i < len(copyValues); i++ {
		for j := i; j > 0 && copyValues[j] < copyValues[j-1]; j-- {
			copyValues[j], copyValues[j-1] = copyValues[j-1], copyValues[j]
		}
	}
	joined := ""
	for _, value := range copyValues {
		joined += value + "\x00"
	}
	return hmacHex([]byte("talaria-mem/purge-inventory-v1"), []byte(joined))
}

func (manager *PurgeManager) Apply(ctx context.Context, receiptID string) (PurgeResult, error) {
	if manager == nil || manager.ReceiptStore == nil || manager.Effects == nil {
		return PurgeResult{}, ErrReceiptInvalid
	}
	manager.mu.Lock()
	if result, ok := manager.completed[receiptID]; ok {
		manager.mu.Unlock()
		return result, nil
	}
	manager.mu.Unlock()
	lockPath := manager.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(manager.ReceiptStore.Dir), ".maintenance.lock")
	}
	var result PurgeResult
	err := WithLock(ctx, lockPath, func(ctx context.Context) error {
		receipt, path, err := manager.ReceiptStore.Claim(ctx, receiptID, func(receipt Receipt) error {
			if receipt.Kind != ReceiptPurge || receipt.MemoryID == "" || receipt.ExpectedRevisionID == "" {
				return ErrReceiptInvalid
			}
			target := targetFromReceipt(receipt)
			if err := target.validate(); err != nil {
				return err
			}
			return manager.Effects.Validate(ctx, target)
		})
		if err != nil {
			// A completed receipt is an idempotent replay.  It is intentionally
			// not returned by Claim, so look it up only after a failed claim.
			if errors.Is(err, ErrReceiptUsed) {
				manager.mu.Lock()
				completed, ok := manager.completed[receiptID]
				manager.mu.Unlock()
				if ok {
					result = completed
					return nil
				}
			}
			return err
		}
		operation := &PurgeOperation{ID: operationID(), ReceiptID: receipt.ID, Target: targetFromReceipt(receipt), Phase: PurgeReserved, UpdatedAt: clockNow(manager.Clock)}
		manager.mu.Lock()
		manager.operations[operation.ID] = operation
		manager.mu.Unlock()
		if err := manager.run(ctx, operation); err != nil {
			return err
		}
		deletion := DeletionReceipt{ID: operationID(), OperationID: operation.ID, MemoryID: operation.Target.MemoryID, CompletedAt: clockNow(manager.Clock)}
		completed, err := manager.ReceiptStore.Complete(ctx, receipt, path)
		if err != nil {
			return err
		}
		_ = completed
		result = PurgeResult{Operation: *operation, DeletionReceipt: deletion}
		manager.mu.Lock()
		manager.completed[receipt.ID] = result
		manager.mu.Unlock()
		return nil
	})
	return result, err
}

func targetFromReceipt(receipt Receipt) PurgeTarget {
	return PurgeTarget{WorkspaceID: receipt.WorkspaceID, MemoryID: receipt.MemoryID, ExpectedRevisionID: receipt.ExpectedRevisionID, ProjectionIDs: append([]string(nil), receipt.ProjectionIDs...), InventoryWatermark: receipt.InventoryWatermark, BackupIDs: append([]string(nil), receipt.BackupIDs...)}
}

func (manager *PurgeManager) run(ctx context.Context, operation *PurgeOperation) error {
	for operation.Phase != PurgeComplete {
		if err := contextErr(ctx); err != nil {
			operation.ResumePhase = operation.Phase
			operation.Phase = PurgeFailed
			operation.SafeError = SafeError(err)
			return err
		}
		if manager.Failpoint != nil {
			operation.ResumePhase = operation.Phase
			if err := manager.Failpoint(operation.Phase); err != nil {
				operation.Phase = PurgeFailed
				operation.SafeError = SafeError(err)
				return err
			}
		}
		var err error
		switch operation.Phase {
		case PurgeReserved:
			err = manager.Effects.MarkPending(ctx, operation.Target, operation.ID)
			if err == nil {
				operation.Phase = PurgePending
			}
		case PurgePending:
			err = manager.Effects.RemoveCanonical(ctx, operation.Target)
			if err == nil {
				operation.Phase = PurgeEffectApplied
			}
		case PurgeEffectApplied:
			err = manager.Effects.CheckpointWAL(ctx)
			if err == nil {
				err = manager.Effects.RebuildProjections(ctx, operation.Target)
			}
			if err == nil {
				operation.Phase = PurgePostEffect
			}
		case PurgePostEffect:
			err = manager.Effects.DeleteBackups(ctx, operation.Target)
			if err == nil {
				err = manager.Effects.MarkComplete(ctx, operation.Target, operation.ID)
			}
			if err == nil {
				operation.Phase = PurgeComplete
			}
		case PurgeFailed:
			return ErrOperationIncomplete
		default:
			return ErrJournalInvalid
		}
		operation.UpdatedAt = clockNow(manager.Clock)
		if err != nil {
			operation.ResumePhase = operation.Phase
			operation.Phase = PurgeFailed
			operation.SafeError = SafeError(err)
			return err
		}
	}
	return nil
}

func (manager *PurgeManager) Resume(ctx context.Context, opID string) (PurgeResult, error) {
	manager.mu.Lock()
	operation := manager.operations[opID]
	manager.mu.Unlock()
	if operation == nil {
		return PurgeResult{}, ErrOperationIncomplete
	}
	if operation.Phase == PurgeComplete {
		manager.mu.Lock()
		result := manager.completed[operation.ReceiptID]
		manager.mu.Unlock()
		return result, nil
	}
	lockPath := manager.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(manager.ReceiptStore.Dir), ".maintenance.lock")
	}
	var result PurgeResult
	err := WithLock(ctx, lockPath, func(ctx context.Context) error {
		if operation.Phase == PurgeFailed {
			if operation.ResumePhase == "" {
				return ErrOperationIncomplete
			}
			operation.Phase = operation.ResumePhase
		}
		if err := manager.run(ctx, operation); err != nil {
			return err
		}
		result = PurgeResult{Operation: *operation, DeletionReceipt: DeletionReceipt{ID: operationID(), OperationID: operation.ID, MemoryID: operation.Target.MemoryID, CompletedAt: clockNow(manager.Clock)}}
		if receipt, path, loadErr := manager.ReceiptStore.Load(ctx, operation.ReceiptID); loadErr == nil && receipt.Status == ReceiptClaimed {
			if _, completeErr := manager.ReceiptStore.Complete(ctx, receipt, path); completeErr != nil {
				return completeErr
			}
		}
		manager.mu.Lock()
		manager.completed[operation.ReceiptID] = result
		manager.mu.Unlock()
		return nil
	})
	return result, err
}

func (manager *PurgeManager) OperationIDForReceipt(receiptID string) string {
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
