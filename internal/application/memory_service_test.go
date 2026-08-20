package application

import (
	"context"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type serviceClock struct{ now time.Time }

func (clock serviceClock) Now() time.Time { return clock.now }

type serviceScanner struct {
	status ports.ScanStatus
	calls  int
}

func (scanner *serviceScanner) Scan(context.Context, []ports.TextField) ports.ScanResult {
	scanner.calls++
	return ports.ScanResult{Status: scanner.status, Generation: "test-generation"}
}

type serviceRepo struct {
	memories  map[string]domain.Memory
	revisions map[string]domain.MemoryRevision
	fts       map[string]bool
	outbox    int
}
type serviceTx struct{ repo *serviceRepo }

func newServiceRepo() *serviceRepo {
	return &serviceRepo{memories: map[string]domain.Memory{}, revisions: map[string]domain.MemoryRevision{}, fts: map[string]bool{}}
}
func (repo *serviceRepo) WithTx(ctx context.Context, operation func(ports.MemoryTx) error) error {
	memories, revisions, fts, outbox := cloneMaps(repo)
	err := operation(&serviceTx{repo: repo})
	if err != nil {
		repo.memories, repo.revisions, repo.fts, repo.outbox = memories, revisions, fts, outbox
	}
	return err
}
func (repo *serviceRepo) ReadCurrent(ctx context.Context, id string) (domain.Memory, domain.MemoryRevision, error) {
	memory, ok := repo.memories[id]
	if !ok {
		return domain.Memory{}, domain.MemoryRevision{}, domain.NewError(domain.CodeNotFound, "memory not found", false)
	}
	revision, ok := repo.revisions[memory.CurrentRevisionID]
	if !ok {
		return domain.Memory{}, domain.MemoryRevision{}, domain.NewError(domain.CodeUnavailable, "revision missing", false)
	}
	return memory, revision, nil
}
func (repo *serviceRepo) RebuildFTS(context.Context) error { return nil }
func (tx *serviceTx) CreateMemory(_ context.Context, memory domain.Memory) error {
	if _, ok := tx.repo.memories[memory.ID]; ok {
		return domain.NewError(domain.CodeRevisionConflict, "duplicate", false)
	}
	tx.repo.memories[memory.ID] = memory
	return nil
}
func (tx *serviceTx) CreateRevision(_ context.Context, revision domain.MemoryRevision) error {
	tx.repo.revisions[revision.ID] = revision
	return nil
}
func (tx *serviceTx) MoveCurrentRevision(_ context.Context, memoryID, expected string, revision domain.MemoryRevision) error {
	memory := tx.repo.memories[memoryID]
	if (expected == "" && memory.CurrentRevisionID != "") || (expected != "" && memory.CurrentRevisionID != expected) {
		return domain.NewError(domain.CodeRevisionConflict, "revision conflict", false)
	}
	memory.CurrentRevisionID, memory.Kind, memory.Trust, memory.Lifecycle, memory.UpdatedAt = revision.ID, revision.Kind, revision.Trust, revision.Lifecycle, revision.CreatedAt
	if revision.Trust != domain.TrustGenerated {
		memory.GeneratedFingerprint = ""
	}
	tx.repo.memories[memoryID] = memory
	return nil
}
func (tx *serviceTx) ReplaceFTSRow(_ context.Context, memoryID string) error {
	delete(tx.repo.fts, memoryID)
	memory := tx.repo.memories[memoryID]
	revision := tx.repo.revisions[memory.CurrentRevisionID]
	if (memory.Trust == domain.TrustVerified || memory.Trust == domain.TrustGenerated) && memory.Lifecycle == domain.LifecycleActive && (revision.Trust == domain.TrustVerified || revision.Trust == domain.TrustGenerated) && revision.Lifecycle == domain.LifecycleActive {
		tx.repo.fts[memoryID] = true
	}
	return nil
}
func (tx *serviceTx) AppendOutbox(_ context.Context, _ ports.OutboxEvent) error {
	tx.repo.outbox++
	return nil
}
func cloneMaps(repo *serviceRepo) (map[string]domain.Memory, map[string]domain.MemoryRevision, map[string]bool, int) {
	memories := map[string]domain.Memory{}
	for key, value := range repo.memories {
		memories[key] = value
	}
	revisions := map[string]domain.MemoryRevision{}
	for key, value := range repo.revisions {
		revisions[key] = value
	}
	fts := map[string]bool{}
	for key, value := range repo.fts {
		fts[key] = value
	}
	return memories, revisions, fts, repo.outbox
}

var _ ports.MemoryRepository = (*serviceRepo)(nil)

func TestMemoryServiceTrustMatrixAndAtomicScan(t *testing.T) {
	repo := newServiceRepo()
	scanner := &serviceScanner{status: ports.ScanClean}
	service := NewMemoryService(repo, scanner, serviceClock{now: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}, nil)
	result, err := service.Create(context.Background(), MutationRequest{Actor: ActorMCP, Caller: "mcp", WorkspaceID: "workspace", Kind: domain.MemoryKindState, Title: "title", Content: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Trust != domain.TrustUnverified {
		t.Fatalf("trust = %q", result.Trust)
	}
	if len(repo.memories) != 1 || repo.outbox != 1 {
		t.Fatalf("mutation counts = %d/%d", len(repo.memories), repo.outbox)
	}
	scanner.status = ports.ScanFinding
	_, err = service.Create(context.Background(), MutationRequest{Actor: ActorCLI, Caller: "cli", WorkspaceID: "workspace", Kind: domain.MemoryKindState, Title: "secret", Content: "unsafe"})
	if !domain.IsCode(err, domain.CodeSecretRefusal) {
		t.Fatalf("finding error = %v", err)
	}
	if len(repo.memories) != 1 {
		t.Fatalf("finding mutated repository")
	}
}

func TestMemoryServiceVerifiedStandingAndIdempotentReplay(t *testing.T) {
	repo := newServiceRepo()
	scanner := &serviceScanner{status: ports.ScanClean}
	service := NewMemoryService(repo, scanner, serviceClock{now: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}, nil)
	request := MutationRequest{Actor: ActorCLI, Caller: "cli", WorkspaceID: "workspace", Kind: domain.MemoryKindStandingInstruction, Title: "instruction", Content: "safe", Verified: true, IdempotencyKey: "same"}
	first, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || replay.MemoryID != first.MemoryID || len(repo.memories) != 1 {
		t.Fatalf("replay = %+v memories=%d", replay, len(repo.memories))
	}
	_, err = service.Create(context.Background(), MutationRequest{Actor: ActorMCP, Caller: "mcp", WorkspaceID: "workspace", Kind: domain.MemoryKindStandingInstruction, Title: "bad", Content: "safe"})
	if !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("standing instruction error = %v", err)
	}
}

func TestMemoryServiceUpdateConflictAndForgetRestore(t *testing.T) {
	repo := newServiceRepo()
	scanner := &serviceScanner{status: ports.ScanClean}
	service := NewMemoryService(repo, scanner, serviceClock{now: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}, nil)
	created, err := service.Create(context.Background(), MutationRequest{Actor: ActorCLI, Caller: "cli", WorkspaceID: "workspace", Kind: domain.MemoryKindFailure, Title: "failure", Content: "body", Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Update(context.Background(), MutationRequest{Actor: ActorCLI, Caller: "cli", MemoryID: created.MemoryID, ExpectedRevisionID: "stale", Title: "new", Content: "body"})
	if !domain.IsCode(err, domain.CodeRevisionConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	forgotten, err := service.Forget(context.Background(), MutationRequest{Actor: ActorCLI, Caller: "cli", MemoryID: created.MemoryID, ExpectedRevisionID: created.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if forgotten.Lifecycle != domain.LifecycleForgotten || repo.fts[created.MemoryID] {
		t.Fatalf("forget result = %+v fts=%v", forgotten, repo.fts[created.MemoryID])
	}
	restored, err := service.Restore(context.Background(), MutationRequest{Actor: ActorCLI, Caller: "cli", MemoryID: created.MemoryID, ExpectedRevisionID: forgotten.RevisionID, Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Lifecycle != domain.LifecycleActive || restored.Trust != domain.TrustVerified {
		t.Fatalf("restore result = %+v", restored)
	}
}

func TestMemoryServiceGeneratedCreationIsServerAssignedAndAtomic(t *testing.T) {
	repo := newServiceRepo()
	scanner := &serviceScanner{status: ports.ScanClean}
	service := NewMemoryService(repo, scanner, serviceClock{now: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}, nil)
	result, err := service.CreateGenerated(context.Background(), GeneratedMutationRequest{WorkspaceID: "workspace", Kind: domain.MemoryKindState, Title: "generated", Content: "safe"}, GeneratedSourceInline)
	if err != nil {
		t.Fatal(err)
	}
	if result.Trust != domain.TrustGenerated {
		t.Fatalf("trust = %q", result.Trust)
	}
	memory, revision, err := repo.ReadCurrent(context.Background(), result.MemoryID)
	if err != nil {
		t.Fatal(err)
	}
	if memory.Pinned || memory.UserGlobal || revision.Provenance.Actor != string(ActorCurator) || revision.Provenance.Source != string(GeneratedSourceInline) {
		t.Fatalf("generated provenance/mutation = %#v %#v", memory, revision)
	}
	if _, err := service.CreateGenerated(context.Background(), GeneratedMutationRequest{WorkspaceID: "workspace", Kind: domain.MemoryKindStandingInstruction, Title: "bad", Content: "safe"}, GeneratedSourceAutomatic); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("standing instruction error = %v", err)
	}
	if _, err := service.CreateGeneratedBatch(context.Background(), []GeneratedMutationRequest{{WorkspaceID: "workspace", Kind: domain.MemoryKindState, Title: "one", Content: "safe"}, {WorkspaceID: "workspace", Kind: domain.MemoryKindStandingInstruction, Title: "two", Content: "bad"}}, GeneratedSourceAutomatic); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("batch error = %v", err)
	}
	if len(repo.memories) != 1 {
		t.Fatalf("failed batch mutated %d memories", len(repo.memories))
	}
}

func TestMemoryServiceConfirmGeneratedClearsGeneratedFingerprint(t *testing.T) {
	repo := newServiceRepo()
	scanner := &serviceScanner{status: ports.ScanClean}
	service := NewMemoryService(repo, scanner, serviceClock{now: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}, nil)
	created, err := service.CreateGenerated(context.Background(), GeneratedMutationRequest{
		WorkspaceID: "workspace", Kind: domain.MemoryKindState, Title: "generated", Content: "safe",
	}, GeneratedSourceAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	memory, revision, err := repo.ReadCurrent(context.Background(), created.MemoryID)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := service.Confirm(context.Background(), MutationRequest{
		Actor:              ActorCLI,
		MemoryID:           memory.ID,
		ExpectedRevisionID: revision.ID,
	})
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if confirmed.Trust != domain.TrustVerified {
		t.Fatalf("confirmed trust = %q", confirmed.Trust)
	}
	confirmedMemory, confirmedRevision, err := repo.ReadCurrent(context.Background(), memory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if confirmedMemory.Trust != domain.TrustVerified || confirmedRevision.Trust != domain.TrustVerified || confirmedMemory.GeneratedFingerprint != "" {
		t.Fatalf("confirmed state = %#v %#v", confirmedMemory, confirmedRevision)
	}
}
