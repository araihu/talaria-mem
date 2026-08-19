package application

import (
	"context"
	"fmt"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type GeneratedSource string

const (
	GeneratedSourceAutomatic GeneratedSource = "automatic"
	GeneratedSourceInline    GeneratedSource = "inline"
)

func (source GeneratedSource) Valid() bool {
	return source == GeneratedSourceAutomatic || source == GeneratedSourceInline
}

type GeneratedMutationRequest struct {
	WorkspaceID     string
	Kind            domain.MemoryKind
	Title           string
	Content         string
	Tags            []string
	ResolutionState domain.ResolutionState
}

func (service *MemoryService) CreateGenerated(ctx context.Context, request GeneratedMutationRequest, source GeneratedSource) (MutationResult, error) {
	results, err := service.CreateGeneratedBatch(ctx, []GeneratedMutationRequest{request}, source)
	if err != nil {
		return MutationResult{}, err
	}
	return results[0], nil
}

func (service *MemoryService) CreateGeneratedBatch(ctx context.Context, requests []GeneratedMutationRequest, source GeneratedSource) ([]MutationResult, error) {
	if !source.Valid() {
		return nil, domain.NewError(domain.CodeValidation, "invalid generated source", false)
	}
	if len(requests) == 0 {
		return nil, domain.NewError(domain.CodeValidation, "generated mutation batch is empty", false)
	}
	now := service.now()
	type prepared struct {
		memory   domain.Memory
		revision domain.MemoryRevision
	}
	preparedRows := make([]prepared, len(requests))
	for index, request := range requests {
		if request.WorkspaceID == "" {
			return nil, domain.NewError(domain.CodeValidation, "generated memory workspace is required", false)
		}
		if request.Kind == domain.MemoryKindStandingInstruction {
			return nil, domain.NewError(domain.CodeValidation, "generated memory cannot be a standing instruction", false)
		}
		if request.Kind != domain.MemoryKindState && request.Kind != domain.MemoryKindProcedure && request.Kind != domain.MemoryKindFailure {
			return nil, domain.NewError(domain.CodeValidation, "invalid generated memory kind", false)
		}
		provenance := domain.Provenance{Actor: string(ActorCurator), Source: string(source)}
		if err := validateMemoryRequest(request.Kind, request.Title, request.Content, request.Tags, provenance); err != nil {
			return nil, err
		}
		if err := service.scanMutation(ctx, MutationRequest{Title: request.Title, Content: request.Content, Tags: request.Tags}); err != nil {
			return nil, err
		}
		resolution, err := domain.ValidateResolutionState(request.Kind, request.ResolutionState)
		if err != nil {
			return nil, err
		}
		memoryID, err := newUUIDv7(now)
		if err != nil {
			return nil, err
		}
		revisionID, err := newUUIDv7(now)
		if err != nil {
			return nil, err
		}
		fingerprint, err := service.generatedFingerprint(ctx, request.Kind, request.Title, request.Content, request.Tags, resolution)
		if err != nil {
			return nil, err
		}
		memory := domain.Memory{ID: memoryID, WorkspaceID: request.WorkspaceID, Kind: request.Kind, Trust: domain.TrustGenerated, Lifecycle: domain.LifecycleActive, GeneratedFingerprint: fingerprint, CreatedAt: now, UpdatedAt: now}
		revision := domain.MemoryRevision{ID: revisionID, MemoryID: memoryID, Number: 1, Kind: request.Kind, Title: request.Title, Content: request.Content, Tags: append([]string(nil), request.Tags...), ResolutionState: resolution, Trust: domain.TrustGenerated, Lifecycle: domain.LifecycleActive, Provenance: provenance, CreatedAt: now}
		preparedRows[index] = prepared{memory: memory, revision: revision}
	}
	if err := service.Repository.WithTx(ctx, func(tx ports.MemoryTx) error {
		for _, row := range preparedRows {
			if err := tx.CreateMemory(ctx, row.memory); err != nil {
				return err
			}
			if err := tx.CreateRevision(ctx, row.revision); err != nil {
				return err
			}
			if err := tx.MoveCurrentRevision(ctx, row.memory.ID, "", row.revision); err != nil {
				return err
			}
			if err := tx.ReplaceFTSRow(ctx, row.memory.ID); err != nil {
				return err
			}
			if err := tx.AppendOutbox(ctx, ports.OutboxEvent{ScopeID: row.memory.WorkspaceID, RevisionWatermark: 0, CreatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	results := make([]MutationResult, len(preparedRows))
	for index, row := range preparedRows {
		results[index] = resultFrom(row.memory, row.revision)
	}
	return results, nil
}

func (service *MemoryService) generatedFingerprint(ctx context.Context, kind domain.MemoryKind, title, content string, tags []string, resolution domain.ResolutionState) (string, error) {
	normalized, err := domain.NormalizeV1(string(kind), title, content, tags)
	if err != nil {
		return "", err
	}
	key := []byte("talaria-mem/generated-fingerprint/v1")
	if service.Deriver != nil {
		key, err = service.Deriver.DeriveKey(ctx, ports.KeyPurposeGeneratedFingerprint, ports.KeyDerivationVersion)
		if err != nil {
			return "", domain.NewError(domain.CodeUnavailable, "generated fingerprint key unavailable", true)
		}
	}
	return keyedDigest(key, fmt.Sprintf("%s\x00resolution=%s", normalized, resolution)), nil
}
