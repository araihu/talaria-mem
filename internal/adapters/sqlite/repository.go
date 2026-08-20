package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite/sqlc"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type Repository struct {
	database *DB
}

func NewRepository(database *DB) *Repository {
	return &Repository{database: database}
}

func (repository *Repository) ActiveGeneratedFingerprintExists(ctx context.Context, workspaceID, fingerprint string) (bool, error) {
	if workspaceID == "" || fingerprint == "" {
		return false, domain.NewError(domain.CodeValidation, "generated fingerprint identity is required", false)
	}
	exists, err := sqlc.New(repository.database.sql).ActiveGeneratedFingerprintExists(ctx, sqlc.ActiveGeneratedFingerprintExistsParams{WorkspaceID: sql.NullString{String: workspaceID, Valid: true}, GeneratedFingerprint: fingerprint})
	if err != nil {
		return false, domain.MapSQLiteError(err)
	}
	return exists == 1, nil
}

func (repository *Repository) WithTx(ctx context.Context, operation func(ports.MemoryTx) error) error {
	return repository.database.withTx(ctx, func(tx *sql.Tx) error {
		return operation(&repositoryTx{queries: sqlc.New(tx)})
	})
}

func (repository *Repository) ReadCurrent(ctx context.Context, memoryID string) (domain.Memory, domain.MemoryRevision, error) {
	row, err := sqlc.New(repository.database.sql).ReadCurrent(ctx, memoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Memory{}, domain.MemoryRevision{}, domain.NewError(domain.CodeNotFound, "memory not found", false)
	}
	if err != nil {
		return domain.Memory{}, domain.MemoryRevision{}, domain.MapSQLiteError(err)
	}
	createdAt, err := parseTimestamp(row.CreatedAt)
	if err != nil {
		return domain.Memory{}, domain.MemoryRevision{}, err
	}
	updatedAt, err := parseTimestamp(row.UpdatedAt)
	if err != nil {
		return domain.Memory{}, domain.MemoryRevision{}, err
	}
	revisionCreatedAt, err := parseTimestamp(row.CreatedAt_2)
	if err != nil {
		return domain.Memory{}, domain.MemoryRevision{}, err
	}
	var tags []string
	if err := json.Unmarshal([]byte(row.TagsJson), &tags); err != nil {
		return domain.Memory{}, domain.MemoryRevision{}, domain.NewError(domain.CodeUnavailable, "stored tags are invalid", false)
	}
	var labels []string
	if err := json.Unmarshal([]byte(row.ProvenanceLabelsJson), &labels); err != nil {
		return domain.Memory{}, domain.MemoryRevision{}, domain.NewError(domain.CodeUnavailable, "stored provenance is invalid", false)
	}

	memory := domain.Memory{
		ID:                   row.ID,
		WorkspaceID:          row.WorkspaceID.String,
		UserGlobal:           row.UserGlobal == 1,
		Kind:                 domain.MemoryKind(row.Kind),
		Trust:                domain.Trust(row.Trust),
		Lifecycle:            domain.Lifecycle(row.Lifecycle),
		CurrentRevisionID:    row.CurrentRevisionID.String,
		GeneratedFingerprint: row.GeneratedFingerprint,
		Pinned:               row.Pinned == 1,
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
	}
	revision := domain.MemoryRevision{
		ID:              row.ID_2,
		MemoryID:        row.ID,
		Number:          row.RevisionNumber,
		Kind:            domain.MemoryKind(row.Kind_2),
		Title:           row.Title,
		Content:         row.Content,
		Tags:            tags,
		ResolutionState: domain.ResolutionState(row.ResolutionState.String),
		Trust:           domain.Trust(row.Trust_2),
		Lifecycle:       domain.Lifecycle(row.Lifecycle_2),
		Provenance: domain.Provenance{
			Actor:         row.ProvenanceActor,
			Source:        row.ProvenanceSource,
			Labels:        labels,
			SourceLocator: row.SourceLocator,
		},
		CreatedAt: revisionCreatedAt,
	}
	resolutionState, err := domain.ValidateResolutionState(revision.Kind, revision.ResolutionState)
	if err != nil {
		return domain.Memory{}, domain.MemoryRevision{}, err
	}
	revision.ResolutionState = resolutionState
	return memory, revision, nil
}

func (repository *Repository) RebuildFTS(ctx context.Context) error {
	return repository.database.withTx(ctx, func(tx *sql.Tx) error {
		queries := sqlc.New(tx)
		if err := queries.DeleteAllFTSRows(ctx); err != nil {
			return err
		}
		return queries.RebuildEligibleFTSRows(ctx)
	})
}

type repositoryTx struct {
	queries *sqlc.Queries
}

func (transaction *repositoryTx) CreateMemory(ctx context.Context, memory domain.Memory) error {
	workspaceID := sql.NullString{String: memory.WorkspaceID, Valid: memory.WorkspaceID != ""}
	currentRevisionID := sql.NullString{String: memory.CurrentRevisionID, Valid: memory.CurrentRevisionID != ""}
	return domain.MapSQLiteError(transaction.queries.CreateMemory(ctx, sqlc.CreateMemoryParams{
		ID:                   memory.ID,
		WorkspaceID:          workspaceID,
		UserGlobal:           boolInt(memory.UserGlobal),
		Kind:                 string(memory.Kind),
		Trust:                string(memory.Trust),
		Lifecycle:            string(memory.Lifecycle),
		CurrentRevisionID:    currentRevisionID,
		GeneratedFingerprint: memory.GeneratedFingerprint,
		Pinned:               boolInt(memory.Pinned),
		CreatedAt:            formatTimestamp(memory.CreatedAt),
		UpdatedAt:            formatTimestamp(memory.UpdatedAt),
	}))
}

func (transaction *repositoryTx) CreateRevision(ctx context.Context, revision domain.MemoryRevision) error {
	resolutionState, err := domain.ValidateResolutionState(revision.Kind, revision.ResolutionState)
	if err != nil {
		return err
	}
	revision.ResolutionState = resolutionState
	if err := domain.ValidateMemoryRevision(revision); err != nil {
		return err
	}
	tags, err := json.Marshal(revision.Tags)
	if err != nil {
		return domain.NewError(domain.CodeValidation, "invalid tags", false)
	}
	labels, err := json.Marshal(revision.Provenance.Labels)
	if err != nil {
		return domain.NewError(domain.CodeValidation, "invalid provenance", false)
	}
	resolution := sql.NullString{String: string(revision.ResolutionState), Valid: revision.ResolutionState != ""}
	return domain.MapSQLiteError(transaction.queries.CreateMemoryRevision(ctx, sqlc.CreateMemoryRevisionParams{
		ID:                   revision.ID,
		MemoryID:             revision.MemoryID,
		RevisionNumber:       revision.Number,
		Kind:                 string(revision.Kind),
		Title:                revision.Title,
		Content:              revision.Content,
		TagsJson:             string(tags),
		ResolutionState:      resolution,
		Trust:                string(revision.Trust),
		Lifecycle:            string(revision.Lifecycle),
		ProvenanceActor:      revision.Provenance.Actor,
		ProvenanceSource:     revision.Provenance.Source,
		ProvenanceLabelsJson: string(labels),
		SourceLocator:        revision.Provenance.SourceLocator,
		CreatedAt:            formatTimestamp(revision.CreatedAt),
	}))
}

func (transaction *repositoryTx) MoveCurrentRevision(ctx context.Context, memoryID, expectedRevisionID string, revision domain.MemoryRevision) error {
	params := sqlc.MoveCurrentRevisionParams{
		CurrentRevisionID:   sql.NullString{String: revision.ID, Valid: true},
		Kind:                string(revision.Kind),
		Trust:               string(revision.Trust),
		Column5:             string(revision.Trust),
		Lifecycle:           string(revision.Lifecycle),
		UpdatedAt:           formatTimestamp(revision.CreatedAt),
		ID:                  memoryID,
		CurrentRevisionID_2: sql.NullString{String: expectedRevisionID, Valid: expectedRevisionID != ""},
	}
	var affected int64
	var err error
	if expectedRevisionID == "" {
		affected, err = transaction.queries.MoveInitialCurrentRevision(ctx, sqlc.MoveInitialCurrentRevisionParams{
			CurrentRevisionID: params.CurrentRevisionID,
			Kind:              params.Kind,
			Trust:             params.Trust,
			Column5:           params.Column5,
			Lifecycle:         params.Lifecycle,
			UpdatedAt:         params.UpdatedAt,
			ID:                params.ID,
		})
	} else {
		affected, err = transaction.queries.MoveCurrentRevision(ctx, params)
	}
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	if affected != 1 {
		return domain.NewError(domain.CodeRevisionConflict, "revision conflict", false)
	}
	return nil
}

func (transaction *repositoryTx) ReplaceFTSRow(ctx context.Context, memoryID string) error {
	if memoryID == "" {
		return domain.NewError(domain.CodeValidation, "FTS row identity is required", false)
	}
	if err := transaction.queries.DeleteFTSRow(ctx, memoryID); err != nil {
		return domain.MapSQLiteError(err)
	}
	// Derive eligibility and fields from canonical rows in this transaction;
	// callers cannot provide content or an eligibility bit.
	return domain.MapSQLiteError(transaction.queries.InsertEligibleFTSRow(ctx, memoryID))
}

func (transaction *repositoryTx) AppendOutbox(ctx context.Context, event ports.OutboxEvent) error {
	return domain.MapSQLiteError(transaction.queries.AppendOutbox(ctx, sqlc.AppendOutboxParams{
		ScopeID: event.ScopeID, RevisionWatermark: event.RevisionWatermark, CreatedAt: formatTimestamp(event.CreatedAt),
	}))
}

func formatTimestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, domain.NewError(domain.CodeUnavailable, "stored timestamp is invalid", false)
	}
	return parsed.UTC(), nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

var _ ports.MemoryRepository = (*Repository)(nil)
var _ ports.MemoryTx = (*repositoryTx)(nil)
