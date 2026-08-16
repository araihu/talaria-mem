package maintenance

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// MemoryActivationJournal is useful for unit/effect tests and for a caller
// that already persists the record in SQLite.  It still enforces the exact
// T2 transition contract, so tests cannot accidentally bless a state machine
// that production recovery would reject.
type MemoryActivationJournal struct {
	mu       sync.RWMutex
	record   ports.ActivationRecord
	blockers []string
}

func NewMemoryActivationJournal(initial ports.ActivationRecord) (*MemoryActivationJournal, error) {
	if initial.Phase == "" {
		initial = DefaultActivationRecord()
	}
	if err := initial.ValidateNoContent(); err != nil {
		return nil, err
	}
	if initial.Version == 0 {
		initial.Version = 1
	}
	return &MemoryActivationJournal{record: initial}, nil
}

func DefaultActivationRecord() ports.ActivationRecord {
	now := time.Now().UTC()
	return ports.ActivationRecord{
		Version:             1,
		ActivationEpoch:     "activation-epoch-1",
		ActiveGeneration:    "betterleaks-active-v1",
		Phase:               ports.ActivationActive,
		ComparativeVerified: true,
		RescanVerified:      true,
		MutationVerified:    true,
		ProjectionVerified:  true,
		ReadinessVerified:   true,
		UpdatedAt:           now,
	}
}

func (journal *MemoryActivationJournal) Load(ctx context.Context) (ports.ActivationRecord, error) {
	if err := contextErr(ctx); err != nil {
		return ports.ActivationRecord{}, err
	}
	if journal == nil {
		return ports.ActivationRecord{}, ErrJournalInvalid
	}
	journal.mu.RLock()
	defer journal.mu.RUnlock()
	return journal.record, nil
}

func (journal *MemoryActivationJournal) Store(ctx context.Context, expectedVersion int64, record ports.ActivationRecord) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if journal == nil {
		return ErrJournalInvalid
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if expectedVersion != journal.record.Version {
		return errors.Join(ErrJournalConflict, ports.ErrActivationCAS)
	}
	if err := ports.ValidateActivationRecordTransition(journal.record, record); err != nil {
		return errors.Join(ErrJournalInvalid, err)
	}
	record.Version = journal.record.Version + 1
	record.UpdatedAt = time.Now().UTC()
	if err := record.ValidateNoContent(); err != nil {
		return err
	}
	journal.record = record
	return nil
}

func (journal *MemoryActivationJournal) SetBlockers(blockers ...string) error {
	if journal == nil {
		return ErrJournalInvalid
	}
	clean := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		if err := validateMetadata(blocker, 128, true); err != nil {
			return err
		}
		clean = append(clean, blocker)
	}
	journal.mu.Lock()
	journal.blockers = clean
	journal.mu.Unlock()
	return nil
}

func (journal *MemoryActivationJournal) ReadinessBlockers(ctx context.Context) ([]string, error) {
	record, err := journal.Load(ctx)
	if err != nil {
		return nil, err
	}
	journal.mu.RLock()
	blockers := append([]string(nil), journal.blockers...)
	journal.mu.RUnlock()
	if record.Phase != ports.ActivationActive || !record.ReadinessVerified || record.LiveMutationStarted {
		blockers = append(blockers, "rule activation pending")
	}
	if record.Phase == ports.ActivationFailed || record.Phase == ports.ActivationRollback {
		blockers = append(blockers, "rule activation recovery failed")
	}
	return uniqueStrings(blockers), nil
}

var _ ports.ActivationJournal = (*MemoryActivationJournal)(nil)

// FileActivationJournal is the restart boundary used when SQLite-backed
// composition is not available.  The file contains the same non-content
// record as the T3 table and is always replaced through a managed temporary
// file; callers still hold the global maintenance lock around state-machine
// effects.
type FileActivationJournal struct {
	Path  string
	Clock ports.Clock
	mu    sync.Mutex
}

func NewFileActivationJournal(path string, initial ports.ActivationRecord) (*FileActivationJournal, error) {
	if err := validateManagedParent(path); err != nil {
		return nil, err
	}
	if initial.Phase == "" {
		initial = DefaultActivationRecord()
	}
	if err := initial.ValidateNoContent(); err != nil {
		return nil, err
	}
	journal := &FileActivationJournal{Path: path}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		initial.Version = maxInt64(initial.Version, 1)
		if _, err := writeManaged(context.Background(), path, initial, nil); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return journal, nil
}

func (journal *FileActivationJournal) Load(ctx context.Context) (ports.ActivationRecord, error) {
	if err := contextErr(ctx); err != nil {
		return ports.ActivationRecord{}, err
	}
	if journal == nil || journal.Path == "" {
		return ports.ActivationRecord{}, ErrJournalInvalid
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return journal.loadLocked()
}

func (journal *FileActivationJournal) loadLocked() (ports.ActivationRecord, error) {
	if err := validateManagedParent(journal.Path); err != nil {
		return ports.ActivationRecord{}, err
	}
	var record ports.ActivationRecord
	if _, err := readManaged(journal.Path, &record); err != nil {
		return ports.ActivationRecord{}, err
	}
	if err := record.ValidateNoContent(); err != nil {
		return ports.ActivationRecord{}, err
	}
	return record, nil
}

func (journal *FileActivationJournal) Store(ctx context.Context, expectedVersion int64, record ports.ActivationRecord) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if journal == nil {
		return ErrJournalInvalid
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	previous, err := journal.loadLocked()
	if err != nil {
		return err
	}
	if previous.Version != expectedVersion {
		return errors.Join(ErrJournalConflict, ports.ErrActivationCAS)
	}
	if err := ports.ValidateActivationRecordTransition(previous, record); err != nil {
		return errors.Join(ErrJournalInvalid, err)
	}
	record.Version = previous.Version + 1
	record.UpdatedAt = clockNow(journal.Clock)
	old, err := fingerprint(journal.Path)
	if err != nil {
		return err
	}
	_, err = writeManaged(ctx, journal.Path, record, &old)
	return err
}

func (journal *FileActivationJournal) ReadinessBlockers(ctx context.Context) ([]string, error) {
	record, err := journal.Load(ctx)
	if err != nil {
		return nil, err
	}
	blockers := make([]string, 0, 2)
	if record.Phase != ports.ActivationActive || !record.ReadinessVerified || record.LiveMutationStarted {
		blockers = append(blockers, "rule activation pending")
	}
	if record.Phase == ports.ActivationFailed || record.Phase == ports.ActivationRollback {
		blockers = append(blockers, "rule activation recovery failed")
	}
	return uniqueStrings(blockers), nil
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

var _ ports.ActivationJournal = (*FileActivationJournal)(nil)

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
