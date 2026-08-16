package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/scanner"
	"github.com/guilhermecastro/talaria-mem/internal/testutil"
)

func managedTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testReceipts(t *testing.T, state string) *ReceiptStore {
	t.Helper()
	store, err := NewReceiptStore(state, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestMaintenanceLockIsExclusiveAndReleases(t *testing.T) {
	dir := managedTestDir(t)
	path := filepath.Join(dir, "maintenance.lock")
	first, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := AcquireLock(context.Background(), path); err == nil || !containsMaintenanceLock(err) {
		t.Fatalf("second lock error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
}

func TestMaintenanceJournalsNeverEchoErrorText(t *testing.T) {
	const canary = "password=canary-secret"
	if got := SafeError(errors.New(canary)); got == canary || strings.Contains(got, "canary") {
		t.Fatalf("safe error leaked input: %q", got)
	}
}

func containsMaintenanceLock(err error) bool {
	return err != nil && (err.Error() == "maintenance lock held" || err.Error() == "maintenance lock unavailable" || errors.Is(err, ErrLockHeld))
}

func TestReceiptIsAuthenticatedBoundAndSingleUse(t *testing.T) {
	dir := managedTestDir(t)
	clock := testutil.NewFixedClock(time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	store := testReceipts(t, dir)
	store.Clock = clock
	receipt, path, err := store.Create(context.Background(), Receipt{ID: "test-receipt", Kind: ReceiptBackupReconcile, Path: filepath.Join(dir, "unknown.db"), Action: "quarantine"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ID == "" || path != filepath.Join(dir, "receipts", receipt.ID+".json") {
		t.Fatalf("unexpected receipt identity/path: %q %q", receipt.ID, path)
	}
	clock.Advance(ReceiptTTL + time.Second)
	if _, _, err := store.Load(context.Background(), receipt.ID); !errors.Is(err, ErrReceiptExpired) {
		t.Fatalf("expired unused receipt error = %v", err)
	}
	clock.Advance(-(ReceiptTTL + time.Second))
	claimed, claimedPath, err := store.Claim(context.Background(), receipt.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != ReceiptClaimed || claimedPath != path {
		t.Fatalf("claim = %#v %q", claimed, claimedPath)
	}
	if _, _, err := store.Claim(context.Background(), receipt.ID, nil); !errors.Is(err, ErrReceiptUsed) {
		t.Fatalf("second claim error = %v", err)
	}
	if _, err := store.Complete(context.Background(), claimed, path); err != nil {
		t.Fatal(err)
	}
}

type snapshotFixture struct {
	source string
}

func (fixture snapshotFixture) Snapshot(ctx context.Context, destination string) error {
	data, err := os.ReadFile(fixture.source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, ManagedFileMode.Perm())
}
func (snapshotFixture) Integrity(context.Context, string) error          { return nil }
func (snapshotFixture) SchemaVersion(context.Context) (int64, error)     { return BackupSchemaVersion, nil }
func (snapshotFixture) RevisionWatermark(context.Context) (int64, error) { return 42, nil }

func TestBackupPendingCompleteAndUnknownNeverDeleted(t *testing.T) {
	dir := managedTestDir(t)
	source := filepath.Join(filepath.Dir(dir), "source.db")
	if err := os.WriteFile(source, []byte("fixture database"), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	manager, err := NewBackupManager(dir, []byte("0123456789abcdef0123456789abcdef"), nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := manager.Create(context.Background(), BackupRequest{CreationCause: "test"}, snapshotFixture{source: source})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Lifecycle != BackupComplete || manifest.SHA256 == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	unknown := filepath.Join(dir, "unknown.db")
	if err := os.WriteFile(unknown, []byte("unmanaged"), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	inventory, err := manager.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Ready || len(inventory.Unknown) != 1 {
		t.Fatalf("inventory = %#v", inventory)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unknown entry was removed", err)
	}
}

func TestBackupReconcileReceiptDriftAndApplyConvergence(t *testing.T) {
	root := managedTestDir(t)
	backupDir := filepath.Join(root, "backups")
	if err := os.Mkdir(backupDir, ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(backupDir, "unknown.db")
	if err := os.WriteFile(unknown, []byte("unmanaged"), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	manager, err := NewBackupManager(backupDir, []byte("0123456789abcdef0123456789abcdef"), nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetReceiptStore(testReceipts(t, root)).SetLockPath(filepath.Join(root, "maintenance.lock"))
	receipt, _, err := manager.ReconcileDryRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, []byte("drift"), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileApply(context.Background(), receipt.ID); !errors.Is(err, ErrReceiptDrift) {
		t.Fatalf("drift apply = %v", err)
	}
	if err := os.WriteFile(unknown, []byte("unmanaged"), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	// A fresh dry-run is required after drift; no invalid receipt may create a
	// journal or mutate the path.
	receipt, _, err = manager.ReconcileDryRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ReconcileApply(context.Background(), receipt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unknown); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reconciled unknown still present: %v", err)
	}
}

func TestBackupReconcilePostEffectResumesFromRecordedPhase(t *testing.T) {
	root := managedTestDir(t)
	backupDir := filepath.Join(root, "backups")
	if err := os.Mkdir(backupDir, ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(backupDir, "unknown.db")
	if err := os.WriteFile(unknown, []byte("unmanaged"), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	manager, err := NewBackupManager(backupDir, []byte("0123456789abcdef0123456789abcdef"), nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetReceiptStore(testReceipts(t, root)).SetLockPath(filepath.Join(root, "maintenance.lock"))
	receipt, _, err := manager.ReconcileDryRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manager.Failpoint = func(phase string) error {
		if phase == string(ReconcileQuarantineRenamed) {
			return errors.New("injected post-effect crash")
		}
		return nil
	}
	if err := manager.ReconcileApply(context.Background(), receipt.ID); err == nil {
		t.Fatal("post-effect failpoint did not interrupt operation")
	}
	opID := manager.OperationIDForReceipt(receipt.ID)
	if opID == "" {
		t.Fatal("operation journal identity was not retained")
	}
	manager.Failpoint = nil
	if err := manager.ResumeReconcile(context.Background(), opID); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := manager.ReceiptStore.Load(context.Background(), receipt.ID)
	if err != nil || loaded.Status != ReceiptComplete {
		t.Fatalf("receipt=%#v load=%v", loaded, err)
	}
}

type purgeFixture struct {
	mu     sync.Mutex
	steps  []string
	called bool
}

func (fixture *purgeFixture) add(step string) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.steps = append(fixture.steps, step)
}
func (fixture *purgeFixture) Validate(context.Context, PurgeTarget) error { return nil }
func (fixture *purgeFixture) MarkPending(context.Context, PurgeTarget, string) error {
	fixture.add("pending")
	return nil
}
func (fixture *purgeFixture) RemoveCanonical(context.Context, PurgeTarget) error {
	fixture.add("canonical")
	fixture.called = true
	return nil
}
func (fixture *purgeFixture) CheckpointWAL(context.Context) error { fixture.add("wal"); return nil }
func (fixture *purgeFixture) RebuildProjections(context.Context, PurgeTarget) error {
	fixture.add("projection")
	return nil
}
func (fixture *purgeFixture) DeleteBackups(context.Context, PurgeTarget) error {
	fixture.add("backups")
	return nil
}
func (fixture *purgeFixture) MarkComplete(context.Context, PurgeTarget, string) error {
	fixture.add("complete")
	return nil
}

func TestPurgeDoesNotRequireScannerAndIsIdempotent(t *testing.T) {
	dir := managedTestDir(t)
	store := testReceipts(t, dir)
	effects := &purgeFixture{}
	manager := NewPurgeManager(effects, store, filepath.Join(dir, "maintenance.lock"), nil)
	receipt, _, err := manager.DryRun(context.Background(), PurgeTarget{MemoryID: "memory-1", ExpectedRevisionID: "revision-1"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Apply(context.Background(), receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Apply(context.Background(), receipt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeletionReceipt.ID == "" || second.DeletionReceipt.ID != first.DeletionReceipt.ID || !effects.called {
		t.Fatalf("first=%#v second=%#v effects=%#v", first, second, effects)
	}
}

func TestMigrationRejectsVacuumAndRecordsFailure(t *testing.T) {
	if err := ValidateMigrationSQL("004_bad.up.sql", []byte("VACUUM")); !errors.Is(err, ErrMigrationRejected) {
		t.Fatalf("vacuum validation = %v", err)
	}
	if err := ValidateMigrationSet([]MigrationStep{{Version: 2, Name: "001_wrong.up.sql", SQL: []byte("CREATE TABLE x (id INTEGER)")}}); !errors.Is(err, ErrMigrationRejected) {
		t.Fatalf("version validation = %v", err)
	}
}

type restoreFixture struct{ installed bool }

func (fixture *restoreFixture) Verify(context.Context, string) error { return nil }
func (fixture *restoreFixture) Fields(context.Context, string) ([]ports.TextField, error) {
	return []ports.TextField{{Name: ports.FieldContent, Value: "safe"}}, nil
}
func (fixture *restoreFixture) Install(context.Context, string) error {
	fixture.installed = true
	return nil
}
func (fixture *restoreFixture) RebuildInventory(context.Context) error { return nil }

type restoreScanner struct{ status ports.ScanStatus }

func (scanner restoreScanner) Scan(context.Context, []ports.TextField) ports.ScanResult {
	return ports.ScanResult{Status: scanner.status}
}

func TestRestoreScannerUnavailableLeavesReceiptUnused(t *testing.T) {
	dir := managedTestDir(t)
	backupDir := filepath.Join(dir, "backups")
	if err := os.Mkdir(backupDir, ManagedDirectoryMode.Perm()); err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	backupManager, err := NewBackupManager(backupDir, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.db")
	if err := os.WriteFile(source, []byte("safe"), ManagedFileMode.Perm()); err != nil {
		t.Fatal(err)
	}
	manifest, err := backupManager.Create(context.Background(), BackupRequest{CreationCause: "restore-test"}, snapshotFixture{source: source})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewInventoryReader(backupDir, key)
	if err != nil {
		t.Fatal(err)
	}
	receipts := testReceipts(t, dir)
	fixture := &restoreFixture{}
	manager := NewRestoreManager(reader, receipts, fixture, restoreScanner{status: ports.ScanError}, filepath.Join(dir, "maintenance.lock"), nil)
	receipt, _, err := manager.DryRun(context.Background(), manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), receipt.ID); !errors.Is(err, ErrScannerUnavailable) {
		t.Fatalf("restore error = %v", err)
	}
	loaded, _, loadErr := receipts.Load(context.Background(), receipt.ID)
	if loadErr != nil || loaded.Status != ReceiptUnused || fixture.installed {
		t.Fatalf("receipt=%#v load=%v installed=%v", loaded, loadErr, fixture.installed)
	}
}

type fakeCandidate struct {
	identity scanner.CandidateIdentity
	status   ports.ScanStatus
}

func (candidate fakeCandidate) Identity() scanner.CandidateIdentity { return candidate.identity }
func (candidate fakeCandidate) Scan(context.Context, []ports.TextField) ports.ScanResult {
	return ports.ScanResult{Status: candidate.status, Generation: candidate.identity.Generation}
}
func (candidate fakeCandidate) ScanBatch(ctx context.Context, batch []scanner.CandidateBatchItem) ([]scanner.CandidateBatchResult, error) {
	result := make([]scanner.CandidateBatchResult, 0, len(batch))
	for _, item := range batch {
		result = append(result, scanner.CandidateBatchResult{ID: item.ID, Result: candidate.Scan(ctx, item.Fields)})
	}
	return result, nil
}
func (candidate fakeCandidate) Compare(context.Context, []scanner.ComparativeFixture, time.Duration) error {
	return nil
}

type ruleMutatorFixture struct{ findings int }

func (fixture *ruleMutatorFixture) Quarantine(context.Context, string, ports.ScanResult) error {
	fixture.findings++
	return nil
}
func (*ruleMutatorFixture) RemoveFTS(context.Context, string) error    { return nil }
func (*ruleMutatorFixture) AppendOutbox(context.Context, string) error { return nil }

type ruleProjectionFixture struct{ rebuilt bool }

func (fixture *ruleProjectionFixture) Rebuild(context.Context, int64) error {
	fixture.rebuilt = true
	return nil
}
func (*ruleProjectionFixture) Verify(context.Context, int64) error { return nil }

func TestRuleUpgradeActivatesOnlyAfterComparativeRescanAndProjection(t *testing.T) {
	root := managedTestDir(t)
	journal, err := NewMemoryActivationJournal(DefaultActivationRecord())
	if err != nil {
		t.Fatal(err)
	}
	receipts := testReceipts(t, root)
	identity := scanner.CandidateIdentity{Generation: "candidate-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Fingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	candidate := fakeCandidate{identity: identity, status: ports.ScanClean}
	source := SliceRuleUpgradeSource{Watermark: 7, Values: []scanner.CandidateBatchItem{{ID: "memory-1", Fields: []ports.TextField{{Name: ports.FieldContent, Value: "safe"}}}}}
	mutator := &ruleMutatorFixture{}
	projection := &ruleProjectionFixture{}
	manager := NewRuleUpgradeManager(journal, source, mutator, projection, receipts, filepath.Join(root, "maintenance.lock"), nil)
	receipt, _, err := manager.DryRun(context.Background(), RuleUpgradeRequest{Candidate: candidate, Fixtures: []scanner.ComparativeFixture{{Name: "clean", Fields: source.Values[0].Fields, Expected: ports.ScanClean}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), receipt.ID, RuleUpgradeRequest{Candidate: candidate, Fixtures: []scanner.ComparativeFixture{{Name: "clean", Fields: source.Values[0].Fields, Expected: ports.ScanClean}}}); err != nil {
		t.Fatal(err)
	}
	record, err := journal.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != ports.ActivationActive || record.ActiveGeneration != identity.Generation || !projection.rebuilt || mutator.findings != 0 {
		t.Fatalf("record=%#v projection=%#v mutator=%#v", record, projection, mutator)
	}
}

func TestRuleUpgradeFindingIsMonotonicAndQuarantinesBeforeActivation(t *testing.T) {
	root := managedTestDir(t)
	journal, err := NewMemoryActivationJournal(DefaultActivationRecord())
	if err != nil {
		t.Fatal(err)
	}
	receipts := testReceipts(t, root)
	identity := scanner.CandidateIdentity{Generation: "candidate-abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd", Fingerprint: "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"}
	candidate := fakeCandidate{identity: identity, status: ports.ScanFinding}
	fields := []ports.TextField{{Name: ports.FieldContent, Value: "redacted"}}
	source := SliceRuleUpgradeSource{Watermark: 9, Values: []scanner.CandidateBatchItem{{ID: "memory-2", Fields: fields}}}
	mutator := &ruleMutatorFixture{}
	projection := &ruleProjectionFixture{}
	manager := NewRuleUpgradeManager(journal, source, mutator, projection, receipts, filepath.Join(root, "maintenance.lock"), nil)
	request := RuleUpgradeRequest{Candidate: candidate, Fixtures: []scanner.ComparativeFixture{{Name: "finding", Fields: fields, Expected: ports.ScanFinding}}}
	receipt, _, err := manager.DryRun(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(context.Background(), receipt.ID, request); err != nil {
		t.Fatal(err)
	}
	record, err := journal.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != ports.ActivationActive || record.ActiveGeneration != identity.Generation || record.MutationCounts.Quarantined != 1 || record.MutationCounts.FTSRemoved != 1 || record.MutationCounts.OutboxAdded != 1 || mutator.findings != 1 {
		t.Fatalf("record=%#v mutator=%#v", record, mutator)
	}
}

func TestActivationJournalPersistsCASAndReadiness(t *testing.T) {
	dir := managedTestDir(t)
	path := filepath.Join(dir, "activation.json")
	journal, err := NewFileActivationJournal(path, DefaultActivationRecord())
	if err != nil {
		t.Fatal(err)
	}
	record, err := journal.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != ports.ActivationActive {
		t.Fatalf("record=%#v", record)
	}
	next := record
	next.Phase = ports.ActivationPending
	next.CandidateGeneration = "candidate-" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	next.CandidateRuleFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	next.ActivationEpoch = "epoch-2"
	next.ComparativeVerified, next.RescanVerified, next.MutationVerified = false, false, false
	next.ProjectionVerified, next.ReadinessVerified = false, false
	if err := journal.Store(context.Background(), record.Version, next); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ReadinessBlockers(context.Background()); err == nil { /* blockers are expected, not an error */
	}
	if err := journal.Store(context.Background(), record.Version, next); !errors.Is(err, ErrJournalConflict) {
		t.Fatalf("stale CAS=%v", err)
	}
}
