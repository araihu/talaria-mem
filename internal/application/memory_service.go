package application

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

// MutationRequest is shared by CLI, MCP, and import adapters. Adapters set
// Actor explicitly; no caller outside CLI can request verified trust.
type MutationRequest struct {
	Actor              Actor
	Caller             string
	WorkspaceID        string
	UserGlobal         bool
	MemoryID           string
	ExpectedRevisionID string
	IdempotencyKey     string
	Kind               domain.MemoryKind
	Title              string
	Content            string
	Tags               []string
	ResolutionState    domain.ResolutionState
	Verified           bool
	Pinned             bool
	Provenance         domain.Provenance
}

type MutationResult struct {
	MemoryID        string
	RevisionID      string
	RevisionNumber  int64
	WorkspaceID     string
	Trust           domain.Trust
	Lifecycle       domain.Lifecycle
	ResolutionState domain.ResolutionState
	Replayed        bool
}

type ReviewResult struct {
	MemoryID        string
	RevisionID      string
	RevisionNumber  int64
	WorkspaceID     string
	Kind            domain.MemoryKind
	Trust           domain.Trust
	Lifecycle       domain.Lifecycle
	Title           string
	Content         string
	Tags            []string
	ResolutionState domain.ResolutionState
}

type Explanation struct {
	MemoryID        string
	RevisionID      string
	Trust           domain.Trust
	Lifecycle       domain.Lifecycle
	Pinned          bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ResolutionState domain.ResolutionState
}

// Optional transaction extensions let the SQLite adapter expose operations
// whose SQL is intentionally not part of the narrow base mutation port.
type pinTx interface {
	SetPinned(ctx context.Context, memoryID, expectedRevisionID string, pinned bool) error
}

type MemoryService struct {
	Repository         ports.MemoryRepository
	Scanner            ports.Scanner
	Clock              ports.Clock
	Idempotency        *Idempotency
	DurableIdempotency IdempotencyStore
	Guard              *ContentOutputGuard
	Deriver            ports.KeyDeriver
	mu                 sync.Mutex
}

func NewMemoryService(repository ports.MemoryRepository, scanner ports.Scanner, clock ports.Clock, deriver ports.KeyDeriver) *MemoryService {
	idempotency := NewIdempotency(clock, deriver)
	service := &MemoryService{Repository: repository, Scanner: scanner, Clock: clock, Idempotency: idempotency, Deriver: deriver,
		Guard: NewContentOutputGuard(scanner, nil, clock)}
	if durable, ok := repository.(IdempotencyStore); ok {
		service.DurableIdempotency = durable
	}
	return service
}

// NewService is kept as a concise constructor for adapter composition.
func NewService(repository ports.MemoryRepository, scanner ports.Scanner, clock ports.Clock, deriver ports.KeyDeriver) *MemoryService {
	return NewMemoryService(repository, scanner, clock, deriver)
}

func (service *MemoryService) Create(ctx context.Context, request MutationRequest) (MutationResult, error) {
	request.Actor = normalizeActor(request.Actor)
	if request.Caller == "" {
		request.Caller = string(request.Actor)
	}
	if err := validateCreateRequest(request); err != nil {
		return MutationResult{}, err
	}
	trust, err := validateTrustKind(request.Actor, request.Kind, request.Verified)
	if err != nil {
		return MutationResult{}, err
	}
	if request.Pinned && trust != domain.TrustVerified {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "only verified memory may be pinned", false)
	}
	if result, replay, err := service.lookupIdempotency(ctx, request, OperationCreate); err != nil || replay {
		return result, err
	}
	if err := service.scanMutation(ctx, request); err != nil {
		return MutationResult{}, err
	}
	now := service.now()
	memoryID, err := newUUIDv7(now)
	if err != nil {
		return MutationResult{}, err
	}
	revisionID, err := newUUIDv7(now)
	if err != nil {
		return MutationResult{}, err
	}
	resolution, err := domain.ValidateResolutionState(request.Kind, request.ResolutionState)
	if err != nil {
		return MutationResult{}, err
	}
	memory := domain.Memory{ID: memoryID, WorkspaceID: request.WorkspaceID, UserGlobal: request.UserGlobal,
		Kind: request.Kind, Trust: trust, Lifecycle: domain.LifecycleActive, Pinned: request.Pinned, CreatedAt: now, UpdatedAt: now}
	revision := domain.MemoryRevision{ID: revisionID, MemoryID: memoryID, Number: 1, Kind: request.Kind,
		Title: request.Title, Content: request.Content, Tags: append([]string(nil), request.Tags...), ResolutionState: resolution,
		Trust: trust, Lifecycle: domain.LifecycleActive, Provenance: request.Provenance, CreatedAt: now}
	scope := request.WorkspaceID
	if request.UserGlobal {
		scope = "global"
	}
	err = service.Repository.WithTx(ctx, func(tx ports.MemoryTx) error {
		if err := tx.CreateMemory(ctx, memory); err != nil {
			return err
		}
		if err := tx.CreateRevision(ctx, revision); err != nil {
			return err
		}
		if err := tx.MoveCurrentRevision(ctx, memoryID, "", revision); err != nil {
			return err
		}
		if err := tx.ReplaceFTSRow(ctx, memoryID); err != nil {
			return err
		}
		return tx.AppendOutbox(ctx, ports.OutboxEvent{ScopeID: scope, RevisionWatermark: 0, CreatedAt: now})
	})
	if err != nil {
		return MutationResult{}, err
	}
	result := resultFrom(memory, revision)
	if err := service.saveIdempotency(ctx, request, OperationCreate, result, now); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (service *MemoryService) Update(ctx context.Context, request MutationRequest) (MutationResult, error) {
	request.Actor = normalizeActor(request.Actor)
	if request.Caller == "" {
		request.Caller = string(request.Actor)
	}
	if request.MemoryID == "" || request.ExpectedRevisionID == "" {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "memory and expected revision are required", false)
	}
	if result, replay, err := service.lookupIdempotency(ctx, request, OperationUpdate); err != nil || replay {
		return result, err
	}
	currentMemory, currentRevision, err := service.Repository.ReadCurrent(ctx, request.MemoryID)
	if err != nil {
		return MutationResult{}, err
	}
	if currentRevision.ID != request.ExpectedRevisionID {
		return MutationResult{}, domain.NewError(domain.CodeRevisionConflict, "revision conflict", false)
	}
	kind := request.Kind
	if kind == "" {
		kind = currentRevision.Kind
	}
	if !kind.Valid() {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "invalid memory kind", false)
	}
	if request.Title == "" {
		request.Title = currentRevision.Title
	}
	if request.Content == "" {
		request.Content = currentRevision.Content
	}
	if request.Tags == nil {
		request.Tags = append([]string(nil), currentRevision.Tags...)
	}
	if emptyProvenance(request.Provenance) {
		request.Provenance = currentRevision.Provenance
	}
	trust, err := validateTrustKind(request.Actor, kind, request.Verified)
	if err != nil {
		return MutationResult{}, err
	}
	if err := validateMemoryRequest(kind, request.Title, request.Content, request.Tags, request.Provenance); err != nil {
		return MutationResult{}, err
	}
	if err := service.scanMutation(ctx, request); err != nil {
		return MutationResult{}, err
	}
	now := service.now()
	resolution, err := domain.ValidateResolutionState(kind, request.ResolutionState)
	if err != nil {
		return MutationResult{}, err
	}
	if kind == domain.MemoryKindFailure && request.ResolutionState == "" {
		resolution = currentRevision.ResolutionState
		if resolution == "" {
			resolution = domain.ResolutionOpen
		}
	}
	revisionID, err := newUUIDv7(now)
	if err != nil {
		return MutationResult{}, err
	}
	revision := domain.MemoryRevision{ID: revisionID, MemoryID: request.MemoryID, Number: currentRevision.Number + 1,
		Kind: kind, Title: request.Title, Content: request.Content, Tags: append([]string(nil), request.Tags...), ResolutionState: resolution,
		Trust: trust, Lifecycle: domain.LifecycleActive, Provenance: request.Provenance, CreatedAt: now}
	// Keep user-global/workspace identity immutable through content updates.
	currentMemory.Kind, currentMemory.Trust, currentMemory.Lifecycle, currentMemory.UpdatedAt = kind, trust, domain.LifecycleActive, now
	scope := currentMemory.WorkspaceID
	if currentMemory.UserGlobal {
		scope = "global"
	}
	err = service.Repository.WithTx(ctx, func(tx ports.MemoryTx) error {
		if err := tx.CreateRevision(ctx, revision); err != nil {
			return err
		}
		if err := tx.MoveCurrentRevision(ctx, request.MemoryID, request.ExpectedRevisionID, revision); err != nil {
			return err
		}
		if err := tx.ReplaceFTSRow(ctx, request.MemoryID); err != nil {
			return err
		}
		return tx.AppendOutbox(ctx, ports.OutboxEvent{ScopeID: scope, RevisionWatermark: 0, CreatedAt: now})
	})
	if err != nil {
		return MutationResult{}, err
	}
	result := resultFrom(currentMemory, revision)
	if err := service.saveIdempotency(ctx, request, OperationUpdate, result, now); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (service *MemoryService) Confirm(ctx context.Context, request MutationRequest) (MutationResult, error) {
	if request.Actor == "" {
		request.Actor = ActorCLI
	}
	request.Verified = true
	if request.ExpectedRevisionID == "" {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "expected revision is required", false)
	}
	currentMemory, currentRevision, err := service.Repository.ReadCurrent(ctx, request.MemoryID)
	if err != nil {
		return MutationResult{}, err
	}
	if currentRevision.ID != request.ExpectedRevisionID {
		return MutationResult{}, domain.NewError(domain.CodeRevisionConflict, "revision conflict", false)
	}
	request.Title, request.Content, request.Tags, request.Kind, request.Provenance = currentRevision.Title, currentRevision.Content, append([]string(nil), currentRevision.Tags...), currentRevision.Kind, currentRevision.Provenance
	request.ResolutionState = currentRevision.ResolutionState
	request.UserGlobal = currentMemory.UserGlobal
	request.WorkspaceID = currentMemory.WorkspaceID
	return service.Update(ctx, request)
}

func (service *MemoryService) Forget(ctx context.Context, request MutationRequest) (MutationResult, error) {
	request.Actor = normalizeActor(request.Actor)
	if request.Caller == "" {
		request.Caller = string(request.Actor)
	}
	if request.MemoryID == "" || request.ExpectedRevisionID == "" {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "memory and expected revision are required", false)
	}
	if result, replay, err := service.lookupIdempotency(ctx, request, OperationForget); err != nil || replay {
		return result, err
	}
	memory, current, err := service.Repository.ReadCurrent(ctx, request.MemoryID)
	if err != nil {
		return MutationResult{}, err
	}
	if current.ID != request.ExpectedRevisionID {
		return MutationResult{}, domain.NewError(domain.CodeRevisionConflict, "revision conflict", false)
	}
	now := service.now()
	revisionID, err := newUUIDv7(now)
	if err != nil {
		return MutationResult{}, err
	}
	tombstone := current
	tombstone.ID, tombstone.Number, tombstone.CreatedAt = revisionID, current.Number+1, now
	tombstone.Lifecycle = domain.LifecycleForgotten
	tombstone.Trust = domain.TrustUnverified
	scope := memory.WorkspaceID
	if memory.UserGlobal {
		scope = "global"
	}
	err = service.Repository.WithTx(ctx, func(tx ports.MemoryTx) error {
		if err := tx.CreateRevision(ctx, tombstone); err != nil {
			return err
		}
		if err := tx.MoveCurrentRevision(ctx, request.MemoryID, request.ExpectedRevisionID, tombstone); err != nil {
			return err
		}
		if err := tx.ReplaceFTSRow(ctx, request.MemoryID); err != nil {
			return err
		}
		return tx.AppendOutbox(ctx, ports.OutboxEvent{ScopeID: scope, RevisionWatermark: 0, CreatedAt: now})
	})
	if err != nil {
		return MutationResult{}, err
	}
	result := resultFrom(memory, tombstone)
	if err := service.saveIdempotency(ctx, request, OperationForget, result, now); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (service *MemoryService) Restore(ctx context.Context, request MutationRequest) (MutationResult, error) {
	request.Actor = normalizeActor(request.Actor)
	if request.Caller == "" {
		request.Caller = string(request.Actor)
	}
	if request.MemoryID == "" || request.ExpectedRevisionID == "" {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "memory and expected revision are required", false)
	}
	if result, replay, err := service.lookupIdempotency(ctx, request, OperationRestore); err != nil || replay {
		return result, err
	}
	memory, current, err := service.Repository.ReadCurrent(ctx, request.MemoryID)
	if err != nil {
		return MutationResult{}, err
	}
	if current.ID != request.ExpectedRevisionID {
		return MutationResult{}, domain.NewError(domain.CodeRevisionConflict, "revision conflict", false)
	}
	if current.Lifecycle != domain.LifecycleForgotten {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "restore requires forgotten revision", false)
	}
	if request.Kind == "" {
		request.Kind = current.Kind
	}
	request.Title, request.Content, request.Tags, request.Provenance = current.Title, current.Content, append([]string(nil), current.Tags...), current.Provenance
	trust, err := validateTrustKind(request.Actor, request.Kind, request.Verified)
	if err != nil {
		return MutationResult{}, err
	}
	if err := service.scanMutation(ctx, request); err != nil {
		return MutationResult{}, err
	}
	now := service.now()
	revisionID, err := newUUIDv7(now)
	if err != nil {
		return MutationResult{}, err
	}
	resolution := current.ResolutionState
	if resolution == "" && request.Kind == domain.MemoryKindFailure {
		resolution = domain.ResolutionOpen
	}
	revision := current
	revision.ID, revision.Number, revision.CreatedAt, revision.Lifecycle, revision.Trust = revisionID, current.Number+1, now, domain.LifecycleActive, trust
	revision.ResolutionState = resolution
	scope := memory.WorkspaceID
	if memory.UserGlobal {
		scope = "global"
	}
	err = service.Repository.WithTx(ctx, func(tx ports.MemoryTx) error {
		if err := tx.CreateRevision(ctx, revision); err != nil {
			return err
		}
		if err := tx.MoveCurrentRevision(ctx, request.MemoryID, request.ExpectedRevisionID, revision); err != nil {
			return err
		}
		if err := tx.ReplaceFTSRow(ctx, request.MemoryID); err != nil {
			return err
		}
		return tx.AppendOutbox(ctx, ports.OutboxEvent{ScopeID: scope, RevisionWatermark: 0, CreatedAt: now})
	})
	if err != nil {
		return MutationResult{}, err
	}
	result := resultFrom(memory, revision)
	if err := service.saveIdempotency(ctx, request, OperationRestore, result, now); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (service *MemoryService) Pin(ctx context.Context, request MutationRequest) (MutationResult, error) {
	request.Actor = normalizeActor(request.Actor)
	if request.Caller == "" {
		request.Caller = string(request.Actor)
	}
	if request.MemoryID == "" || request.ExpectedRevisionID == "" {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "memory and expected revision are required", false)
	}
	if result, replay, err := service.lookupIdempotency(ctx, request, OperationPin); err != nil || replay {
		return result, err
	}
	memory, current, err := service.Repository.ReadCurrent(ctx, request.MemoryID)
	if err != nil {
		return MutationResult{}, err
	}
	if current.ID != request.ExpectedRevisionID {
		return MutationResult{}, domain.NewError(domain.CodeRevisionConflict, "revision conflict", false)
	}
	if current.Trust != domain.TrustVerified || current.Lifecycle != domain.LifecycleActive {
		return MutationResult{}, domain.NewError(domain.CodeValidation, "only verified active memory may be pinned", false)
	}
	now := service.now()
	var result MutationResult
	err = service.Repository.WithTx(ctx, func(tx ports.MemoryTx) error {
		extended, ok := tx.(pinTx)
		if !ok {
			return domain.NewError(domain.CodeUnavailable, "pin transaction unavailable", true)
		}
		if err := extended.SetPinned(ctx, request.MemoryID, request.ExpectedRevisionID, true); err != nil {
			return err
		}
		scope := memory.WorkspaceID
		if memory.UserGlobal {
			scope = "global"
		}
		if err := tx.AppendOutbox(ctx, ports.OutboxEvent{ScopeID: scope, RevisionWatermark: 0, CreatedAt: now}); err != nil {
			return err
		}
		result = resultFrom(memory, current)
		return nil
	})
	if err != nil {
		return MutationResult{}, err
	}
	if err := service.saveIdempotency(ctx, request, OperationPin, result, now); err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func (service *MemoryService) ReviewUnverified(ctx context.Context, memoryID, workspaceID string) (ReviewResult, error) {
	memory, revision, err := service.Repository.ReadCurrent(ctx, memoryID)
	if err != nil {
		return ReviewResult{}, err
	}
	if workspaceID != "" && memory.WorkspaceID != workspaceID {
		return ReviewResult{}, domain.NewError(domain.CodeNotFound, "memory not found", false)
	}
	if revision.Trust == domain.TrustVerified {
		return ReviewResult{}, domain.NewError(domain.CodeValidation, "memory is already verified", false)
	}
	if service.Guard != nil {
		if _, err := service.Guard.Check(ctx, OutputRequest{Route: ports.FieldCLIRead, WorkspaceID: memory.WorkspaceID, MemoryID: memory.ID, RevisionID: revision.ID, Fields: revisionFields(revision)}); err != nil {
			return ReviewResult{}, err
		}
	}
	return ReviewResult{MemoryID: memory.ID, RevisionID: revision.ID, RevisionNumber: revision.Number, WorkspaceID: memory.WorkspaceID, Kind: revision.Kind, Trust: revision.Trust, Lifecycle: revision.Lifecycle, Title: revision.Title, Content: revision.Content, Tags: append([]string(nil), revision.Tags...), ResolutionState: revision.ResolutionState}, nil
}

func (service *MemoryService) Explain(ctx context.Context, memoryID string) (Explanation, error) {
	memory, revision, err := service.Repository.ReadCurrent(ctx, memoryID)
	if err != nil {
		return Explanation{}, err
	}
	return Explanation{MemoryID: memory.ID, RevisionID: revision.ID, Trust: memory.Trust, Lifecycle: memory.Lifecycle, Pinned: memory.Pinned, CreatedAt: memory.CreatedAt, UpdatedAt: memory.UpdatedAt, ResolutionState: revision.ResolutionState}, nil
}

func (service *MemoryService) GuardContent(ctx context.Context, request OutputRequest) (OutputResult, error) {
	if service.Guard == nil {
		return OutputResult{}, domain.NewError(domain.CodeUnavailable, "content scanner unavailable", true)
	}
	return service.Guard.Check(ctx, request)
}

func validateCreateRequest(request MutationRequest) error {
	if request.MemoryID != "" || request.ExpectedRevisionID != "" {
		return domain.NewError(domain.CodeValidation, "create cannot carry existing identity", false)
	}
	if request.UserGlobal && request.WorkspaceID != "" {
		return domain.NewError(domain.CodeValidation, "global memory cannot carry workspace", false)
	}
	if !request.UserGlobal && request.WorkspaceID == "" {
		return domain.NewError(domain.CodeValidation, "workspace is required", false)
	}
	return validateMemoryRequest(request.Kind, request.Title, request.Content, request.Tags, request.Provenance)
}
func validateMemoryRequest(kind domain.MemoryKind, title, content string, tags []string, provenance domain.Provenance) error {
	if !kind.Valid() {
		return domain.NewError(domain.CodeValidation, "invalid memory kind", false)
	}
	if err := domain.ValidateMemoryText(title, []byte(content), tags); err != nil {
		return err
	}
	return domain.ValidateProvenance(provenance)
}
func (service *MemoryService) scanMutation(ctx context.Context, request MutationRequest) error {
	if service.Scanner == nil {
		return domain.NewError(domain.CodeUnavailable, "content scanner unavailable", true)
	}
	fields := []ports.TextField{{Name: ports.FieldTitle, Value: request.Title}, {Name: ports.FieldContent, Value: request.Content}}
	for _, tag := range request.Tags {
		fields = append(fields, ports.TextField{Name: ports.FieldTag, Value: tag})
	}
	for _, label := range request.Provenance.Labels {
		fields = append(fields, ports.TextField{Name: ports.FieldProvenanceLabel, Value: label})
	}
	if request.Provenance.SourceLocator != "" {
		fields = append(fields, ports.TextField{Name: ports.FieldSourceLocator, Value: request.Provenance.SourceLocator})
	}
	result := service.Scanner.Scan(ctx, fields)
	switch result.Status {
	case ports.ScanClean:
		return nil
	case ports.ScanFinding:
		return domain.NewError(domain.CodeSecretRefusal, "secret detected; memory not stored", false)
	case ports.ScanTimeout:
		return domain.NewError(domain.CodeTimeout, "content scan timed out", true)
	default:
		return domain.NewError(domain.CodeUnavailable, "content scan unavailable", true)
	}
}
func (service *MemoryService) now() time.Time {
	if service.Clock != nil {
		return service.Clock.Now().UTC()
	}
	return time.Now().UTC()
}
func normalizeActor(actor Actor) Actor {
	if actor == "" {
		return ActorCLI
	}
	return actor
}
func resultFrom(memory domain.Memory, revision domain.MemoryRevision) MutationResult {
	return MutationResult{MemoryID: memory.ID, RevisionID: revision.ID, RevisionNumber: revision.Number, WorkspaceID: memory.WorkspaceID, Trust: revision.Trust, Lifecycle: revision.Lifecycle, ResolutionState: revision.ResolutionState}
}
func revisionFields(revision domain.MemoryRevision) []ports.TextField {
	fields := []ports.TextField{{Name: ports.FieldTitle, Value: revision.Title}, {Name: ports.FieldContent, Value: revision.Content}}
	for _, tag := range revision.Tags {
		fields = append(fields, ports.TextField{Name: ports.FieldTag, Value: tag})
	}
	return fields
}

func emptyProvenance(value domain.Provenance) bool {
	return value.Actor == "" && value.Source == "" && len(value.Labels) == 0 && value.SourceLocator == ""
}

func (service *MemoryService) lookupIdempotency(ctx context.Context, request MutationRequest, operation Operation) (MutationResult, bool, error) {
	if request.IdempotencyKey == "" {
		return MutationResult{}, false, nil
	}
	if service.Idempotency == nil {
		return MutationResult{}, false, nil
	}
	if service.DurableIdempotency != nil {
		keyDigest, requestDigest, err := service.Idempotency.Digests(ctx, request.Caller, string(operation), request.IdempotencyKey, request)
		if err != nil {
			return MutationResult{}, false, err
		}
		record, found, _, err := service.DurableIdempotency.LookupIdempotency(ctx, request.Caller, string(operation), keyDigest, requestDigest, service.now())
		if err != nil {
			return MutationResult{}, false, err
		}
		if !found {
			return MutationResult{}, false, nil
		}
		return resultFromRecord(record), true, nil
	}
	record, found, _, err := service.Idempotency.Lookup(ctx, request.Caller, string(operation), request.IdempotencyKey, request, service.now())
	if err != nil {
		return MutationResult{}, false, err
	}
	if !found {
		return MutationResult{}, false, nil
	}
	return resultFromRecord(record), true, nil
}
func (service *MemoryService) saveIdempotency(ctx context.Context, request MutationRequest, operation Operation, result MutationResult, now time.Time) error {
	if request.IdempotencyKey == "" || service.Idempotency == nil {
		return nil
	}
	keyDigest, requestDigest, err := service.Idempotency.Digests(ctx, request.Caller, string(operation), request.IdempotencyKey, request)
	if err != nil {
		return err
	}
	record := IdempotencyRecord{Caller: request.Caller, Operation: string(operation), KeyDigest: keyDigest, RequestDigest: requestDigest, TargetIDs: []string{result.MemoryID, result.RevisionID}, SafeResult: map[string]string{"memory_id": result.MemoryID, "revision_id": result.RevisionID, "trust": string(result.Trust), "lifecycle": string(result.Lifecycle), "workspace_id": result.WorkspaceID}, CreatedAt: now, ExpiresAt: now.Add(domain.IdempotencyTTL)}
	if service.DurableIdempotency != nil {
		return service.DurableIdempotency.SaveIdempotency(ctx, record)
	}
	return service.Idempotency.Save(ctx, record)
}

func (service *MemoryService) CleanupIdempotency(ctx context.Context, now time.Time) error {
	if service.DurableIdempotency != nil {
		return service.DurableIdempotency.CleanupIdempotency(ctx, now)
	}
	if service.Idempotency != nil {
		return service.Idempotency.Cleanup(ctx, now)
	}
	return nil
}
func resultFromRecord(record IdempotencyRecord) MutationResult {
	return MutationResult{MemoryID: record.SafeResult["memory_id"], RevisionID: record.SafeResult["revision_id"], WorkspaceID: record.SafeResult["workspace_id"], Trust: domain.Trust(record.SafeResult["trust"]), Lifecycle: domain.Lifecycle(record.SafeResult["lifecycle"]), Replayed: true}
}

func newUUIDv7(now time.Time) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	milliseconds := uint64(now.UnixMilli())
	binary.BigEndian.PutUint32(bytes[0:4], uint32(milliseconds>>16))
	binary.BigEndian.PutUint16(bytes[4:6], uint16(milliseconds))
	bytes[6] = (bytes[6] & 0x0f) | 0x70
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", binary.BigEndian.Uint32(bytes[0:4]), binary.BigEndian.Uint16(bytes[4:6]), binary.BigEndian.Uint16(bytes[6:8]), binary.BigEndian.Uint16(bytes[8:10]), uint64(bytes[10])<<40|uint64(bytes[11])<<32|uint64(bytes[12])<<24|uint64(bytes[13])<<16|uint64(bytes[14])<<8|uint64(bytes[15])), nil
}
