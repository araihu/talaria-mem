package maintenance

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/scanner"
)

type RuleUpgradeSource interface {
	RevisionWatermark(ctx context.Context) (int64, error)
	Items(ctx context.Context, afterID string) ([]scanner.CandidateBatchItem, error)
}

type RuleUpgradeMutator interface {
	Quarantine(ctx context.Context, id string, result ports.ScanResult) error
	RemoveFTS(ctx context.Context, id string) error
	AppendOutbox(ctx context.Context, id string) error
}

type RuleUpgradeProjection interface {
	Rebuild(ctx context.Context, watermark int64) error
	Verify(ctx context.Context, watermark int64) error
}

type RuleUpgradeRequest struct {
	Candidate scanner.CandidateService
	Fixtures  []scanner.ComparativeFixture
	Watermark int64
}

type RuleUpgradeOperation struct {
	ID        string                `json:"id"`
	ReceiptID string                `json:"receipt_id"`
	Phase     ports.ActivationPhase `json:"phase"`
	UpdatedAt time.Time             `json:"updated_at"`
	SafeError string                `json:"safe_error,omitempty"`
}

type RuleUpgradeManager struct {
	Journal      ports.ActivationJournal
	Source       RuleUpgradeSource
	Mutator      RuleUpgradeMutator
	Projection   RuleUpgradeProjection
	ReceiptStore *ReceiptStore
	LockPath     string
	Clock        ports.Clock
	Failpoint    func(string) error
	mu           sync.Mutex
	operations   map[string]*RuleUpgradeOperation
}

func NewRuleUpgradeManager(journal ports.ActivationJournal, source RuleUpgradeSource, mutator RuleUpgradeMutator, projection RuleUpgradeProjection, receipts *ReceiptStore, lockPath string, clock ports.Clock) *RuleUpgradeManager {
	return &RuleUpgradeManager{Journal: journal, Source: source, Mutator: mutator, Projection: projection, ReceiptStore: receipts, LockPath: lockPath, Clock: clock, operations: make(map[string]*RuleUpgradeOperation)}
}

func (manager *RuleUpgradeManager) DryRun(ctx context.Context, request RuleUpgradeRequest) (Receipt, string, error) {
	if manager == nil || manager.Journal == nil || manager.ReceiptStore == nil || request.Candidate == nil {
		return Receipt{}, "", ErrReceiptInvalid
	}
	identity := request.Candidate.Identity()
	if err := ports.ValidateCandidateIdentity(identity.Generation, identity.Fingerprint); err != nil {
		return Receipt{}, "", err
	}
	record, err := manager.Journal.Load(ctx)
	if err != nil {
		return Receipt{}, "", err
	}
	if record.Phase != ports.ActivationActive || record.CandidateGeneration != "" {
		return Receipt{}, "", ErrOperationIncomplete
	}
	watermark := request.Watermark
	if watermark == 0 && manager.Source != nil {
		watermark, err = manager.Source.RevisionWatermark(ctx)
		if err != nil {
			return Receipt{}, "", err
		}
	}
	if watermark < 0 {
		return Receipt{}, "", ErrReceiptInvalid
	}
	receipt := Receipt{Kind: ReceiptRuleUpgrade, ActiveGeneration: record.ActiveGeneration, CandidateGeneration: identity.Generation, CandidateFingerprint: identity.Fingerprint, RevisionWatermark: watermark, Action: "activate", Scope: "scanner-rules"}
	return manager.ReceiptStore.Create(ctx, receipt)
}

func (manager *RuleUpgradeManager) Apply(ctx context.Context, receiptID string, request RuleUpgradeRequest) error {
	if manager == nil || manager.Journal == nil || manager.ReceiptStore == nil || request.Candidate == nil || manager.Source == nil || manager.Mutator == nil || manager.Projection == nil {
		return ErrReceiptInvalid
	}
	lockPath := manager.LockPath
	if lockPath == "" {
		lockPath = filepath.Join(filepath.Dir(manager.ReceiptStore.Dir), ".maintenance.lock")
	}
	return WithLock(ctx, lockPath, func(ctx context.Context) error {
		receipt, path, err := manager.ReceiptStore.Claim(ctx, receiptID, func(receipt Receipt) error {
			identity := request.Candidate.Identity()
			if receipt.Kind != ReceiptRuleUpgrade || receipt.Action != "activate" || receipt.ActiveGeneration == "" || receipt.CandidateGeneration != identity.Generation || receipt.CandidateFingerprint != identity.Fingerprint {
				return ErrReceiptDrift
			}
			if err := ports.ValidateCandidateIdentity(identity.Generation, identity.Fingerprint); err != nil {
				return err
			}
			record, loadErr := manager.Journal.Load(ctx)
			if loadErr != nil {
				return loadErr
			}
			if record.Phase != ports.ActivationActive || record.ActiveGeneration != receipt.ActiveGeneration {
				return ErrReceiptDrift
			}
			return nil
		})
		if err != nil {
			return err
		}
		op := &RuleUpgradeOperation{ID: operationID(), ReceiptID: receipt.ID, Phase: ports.ActivationPending, UpdatedAt: clockNow(manager.Clock)}
		manager.mu.Lock()
		manager.operations[op.ID] = op
		manager.mu.Unlock()
		if err := manager.run(ctx, op, receipt, request); err != nil {
			return err
		}
		_, err = manager.ReceiptStore.Complete(ctx, receipt, path)
		return err
	})
}

func (manager *RuleUpgradeManager) run(ctx context.Context, operation *RuleUpgradeOperation, receipt Receipt, request RuleUpgradeRequest) error {
	record, err := manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	identity := request.Candidate.Identity()
	// The first durable write is pending + candidate identity.  It is the only
	// edge that introduces candidate bytes into the journal.
	pending := record
	pending.ActivationEpoch, pending.CandidateGeneration, pending.CandidateRuleFingerprint = operation.ID, identity.Generation, identity.Fingerprint
	pending.Phase, pending.RevisionWatermark = ports.ActivationPending, receipt.RevisionWatermark
	pending.LastProcessedID, pending.ResumeCursor = "", ""
	pending.LiveMutationStarted, pending.MutationCounts = false, ports.ActivationMutationCounts{}
	pending.ComparativeVerified, pending.RescanVerified, pending.MutationVerified = false, false, false
	pending.ProjectionVerified, pending.ReadinessVerified, pending.CandidateDiscardReverified = false, false, false
	if err := manager.Journal.Store(ctx, record.Version, pending); err != nil {
		return err
	}
	if err := manager.fail("pending"); err != nil {
		return manager.discard(ctx, pending, err)
	}
	record, err = manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	quiesced := record
	quiesced.Phase = ports.ActivationQuiesced
	if err := manager.Journal.Store(ctx, record.Version, quiesced); err != nil {
		return err
	}
	if err := manager.fail("quiesced"); err != nil {
		return manager.discard(ctx, quiesced, err)
	}
	if err := request.Candidate.Compare(ctx, request.Fixtures, 2*time.Second); err != nil {
		return manager.discard(ctx, quiesced, err)
	}
	record, err = manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	rescanning := record
	rescanning.Phase, rescanning.ComparativeVerified = ports.ActivationRescanning, true
	if err := manager.Journal.Store(ctx, record.Version, rescanning); err != nil {
		return err
	}
	if err := manager.fail("rescanning"); err != nil {
		return manager.discard(ctx, rescanning, err)
	}
	items, err := manager.Source.Items(ctx, "")
	if err != nil {
		return manager.discard(ctx, rescanning, err)
	}
	results, err := request.Candidate.ScanBatch(ctx, items)
	if err != nil {
		return manager.discard(ctx, rescanning, err)
	}
	findings := make([]scanner.CandidateBatchResult, 0)
	for _, result := range results {
		if result.ID == "" || result.Result.Generation != identity.Generation {
			return manager.discard(ctx, rescanning, ErrScannerUnavailable)
		}
		if result.Result.Status == ports.ScanClean {
			continue
		}
		if result.Result.Status != ports.ScanFinding {
			return manager.discard(ctx, rescanning, ErrScannerUnavailable)
		}
		findings = append(findings, result)
	}
	record, err = manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	record.RescanVerified = true
	if len(findings) == 0 {
		// The journal graph deliberately passes through quarantining even for
		// a zero-finding rescan; this makes the pre/post live-mutation boundary
		// identical on restart and prevents a phase-skipping activation.
		record.Phase = ports.ActivationQuarantining
		if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
			return err
		}
		if err := manager.fail("quarantining"); err != nil {
			return manager.discard(ctx, record, err)
		}
		record, err = manager.Journal.Load(ctx)
		if err != nil {
			return err
		}
		record.Phase, record.MutationVerified = ports.ActivationProjectionRebuild, true
		if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
			return err
		}
	} else {
		record.Phase = ports.ActivationQuarantining
		if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
			return err
		}
		if err := manager.fail("quarantining"); err != nil {
			return manager.discard(ctx, record, err)
		}
		record, err = manager.Journal.Load(ctx)
		if err != nil {
			return err
		}
		record.Phase, record.LiveMutationStarted = ports.ActivationLiveMutationStarted, true
		if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
			return err
		}
		if err := manager.fail("live_mutation_started"); err != nil {
			return manager.failLive(ctx, err)
		}
		for _, finding := range findings {
			if err := manager.Mutator.Quarantine(ctx, finding.ID, finding.Result); err != nil {
				return manager.failLive(ctx, err)
			}
			if err := manager.fail("after_quarantine"); err != nil {
				return manager.failLive(ctx, err)
			}
			if err := manager.Mutator.RemoveFTS(ctx, finding.ID); err != nil {
				return manager.failLive(ctx, err)
			}
			if err := manager.fail("after_fts_removal"); err != nil {
				return manager.failLive(ctx, err)
			}
			if err := manager.Mutator.AppendOutbox(ctx, finding.ID); err != nil {
				return manager.failLive(ctx, err)
			}
			if err := manager.fail("after_outbox_append"); err != nil {
				return manager.failLive(ctx, err)
			}
			record, err = manager.Journal.Load(ctx)
			if err != nil {
				return err
			}
			record.MutationCounts.Quarantined++
			record.MutationCounts.FTSRemoved++
			record.MutationCounts.OutboxAdded++
			record.LastProcessedID, record.ResumeCursor = finding.ID, finding.ID
			if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
				return err
			}
		}
		record, err = manager.Journal.Load(ctx)
		if err != nil {
			return err
		}
		record.Phase, record.MutationVerified = ports.ActivationProjectionRebuild, true
		if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
			return err
		}
	}
	if err := manager.fail("projection_rebuild"); err != nil {
		return manager.failBeforeOrAfterLive(ctx, err)
	}
	if err := manager.Projection.Rebuild(ctx, receipt.RevisionWatermark); err != nil {
		return manager.failBeforeOrAfterLive(ctx, err)
	}
	record, err = manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	record.MutationCounts.Projected++
	record.ProjectionVerified = true
	record.ReadinessVerified = true
	if err := manager.Projection.Verify(ctx, receipt.RevisionWatermark); err != nil {
		return manager.failBeforeOrAfterLive(ctx, err)
	}
	if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
		return err
	}
	record, err = manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	record.Phase = ports.ActivationActive
	record.ActiveGeneration = identity.Generation
	record.CandidateGeneration, record.CandidateRuleFingerprint = "", ""
	if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
		return err
	}
	operation.Phase = ports.ActivationActive
	operation.UpdatedAt = clockNow(manager.Clock)
	return nil
}

func (manager *RuleUpgradeManager) discard(ctx context.Context, record ports.ActivationRecord, cause error) error {
	if record.LiveMutationStarted {
		return manager.failLive(ctx, cause)
	}
	record.CandidateGeneration, record.CandidateRuleFingerprint = "", ""
	record.Phase = ports.ActivationCandidateDiscarded
	record.SafeError = SafeError(cause)
	record.CandidateDiscardReverified = true
	record.MutationVerified = true
	record.ProjectionVerified, record.ReadinessVerified = true, true
	if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
		return err
	}
	record, err := manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	record.Phase, record.SafeError = ports.ActivationActive, ""
	return manager.Journal.Store(ctx, record.Version, record)
}

func (manager *RuleUpgradeManager) failLive(ctx context.Context, cause error) error {
	record, err := manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	record.Phase, record.LiveMutationStarted, record.SafeError = ports.ActivationFailed, true, SafeError(cause)
	if storeErr := manager.Journal.Store(ctx, record.Version, record); storeErr != nil {
		return storeErr
	}
	return errors.Join(ErrOperationIncomplete, cause)
}

func (manager *RuleUpgradeManager) failBeforeOrAfterLive(ctx context.Context, cause error) error {
	record, err := manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	if record.LiveMutationStarted || record.Phase == ports.ActivationLiveMutationStarted {
		return manager.failLive(ctx, cause)
	}
	return manager.discard(ctx, record, cause)
}

func (manager *RuleUpgradeManager) fail(phase string) error {
	if manager.Failpoint == nil {
		return nil
	}
	return manager.Failpoint(phase)
}

func (manager *RuleUpgradeManager) Resume(ctx context.Context, request RuleUpgradeRequest) error {
	record, err := manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	if record.Phase != ports.ActivationFailed && record.Phase != ports.ActivationLiveMutationStarted && record.Phase != ports.ActivationProjectionRebuild {
		return nil
	}
	// Resume is deliberately monotonic: it rebuilds the projection and only
	// promotes a candidate after all durable gates are verified.  It never
	// clears quarantine or turns a failed live activation into a rollback.
	if record.CandidateGeneration == "" {
		return ErrOperationIncomplete
	}
	if err := manager.Projection.Rebuild(ctx, record.RevisionWatermark); err != nil {
		return manager.failLive(ctx, err)
	}
	record, err = manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	record.Phase, record.ProjectionVerified, record.ReadinessVerified = ports.ActivationProjectionRebuild, true, true
	if err := manager.Journal.Store(ctx, record.Version, record); err != nil {
		return err
	}
	record, err = manager.Journal.Load(ctx)
	if err != nil {
		return err
	}
	record.Phase, record.ActiveGeneration = ports.ActivationActive, record.CandidateGeneration
	record.CandidateGeneration, record.CandidateRuleFingerprint, record.SafeError = "", "", ""
	return manager.Journal.Store(ctx, record.Version, record)
}

type SliceRuleUpgradeSource struct {
	Watermark int64
	Values    []scanner.CandidateBatchItem
}

func (source SliceRuleUpgradeSource) RevisionWatermark(context.Context) (int64, error) {
	return source.Watermark, nil
}
func (source SliceRuleUpgradeSource) Items(ctx context.Context, afterID string) ([]scanner.CandidateBatchItem, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	result := make([]scanner.CandidateBatchItem, 0, len(source.Values))
	for _, value := range source.Values {
		if afterID == "" || value.ID > afterID {
			result = append(result, value)
		}
	}
	return result, nil
}
