package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite/sqlc"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
	"github.com/guilhermecastro/talaria-mem/internal/projection"
)

// ProjectionStore is the durable source/outbox boundary consumed by the
// deterministic projection worker. It exposes hashes and bounded metadata;
// content is loaded only into the worker's scanner-gated render path.
type ProjectionStore struct {
	database  *DB
	targetDir string
}

func NewProjectionStore(database *DB, targetDir string) *ProjectionStore {
	return &ProjectionStore{database: database, targetDir: targetDir}
}

func (store *ProjectionStore) LoadScope(ctx context.Context, scopeID string) (projection.ScopeDocument, error) {
	if scopeID == "" {
		return projection.ScopeDocument{}, domain.NewError(domain.CodeValidation, "projection scope is required", false)
	}
	queries, err := store.queries()
	if err != nil {
		return projection.ScopeDocument{}, err
	}
	if scopeID != "global" {
		if _, err := queries.ReadWorkspace(ctx, scopeID); err != nil {
			if err == sql.ErrNoRows {
				return projection.ScopeDocument{}, domain.NewError(domain.CodeNotFound, "workspace not found", false)
			}
			return projection.ScopeDocument{}, domain.MapSQLiteError(err)
		}
	}
	ids, err := queries.ListProjectionMemoryIDs(ctx, sqlc.ListProjectionMemoryIDsParams{
		Column1:     scopeID,
		WorkspaceID: sql.NullString{String: scopeID, Valid: scopeID != "global"},
	})
	if err != nil {
		return projection.ScopeDocument{}, domain.MapSQLiteError(err)
	}
	repository := NewRepository(store.database)
	memories := make([]projection.MemoryDocument, 0, len(ids))
	for _, memoryID := range ids {
		memory, revision, err := repository.ReadCurrent(ctx, memoryID)
		if err != nil {
			return projection.ScopeDocument{}, err
		}
		memories = append(memories, projection.MemoryDocument{Memory: memory, Revision: revision})
	}
	if store.targetDir == "" {
		return projection.ScopeDocument{}, domain.NewError(domain.CodeUnavailable, "projection target directory unavailable", true)
	}
	return projection.ScopeDocument{ScopeID: scopeID, TargetPath: projectionPath(store.targetDir, scopeID), Memories: memories}, nil
}

func (store *ProjectionStore) PendingProjection(ctx context.Context, scopeID string, now time.Time) ([]ports.ProjectionRequest, error) {
	if scopeID == "" {
		return nil, domain.NewError(domain.CodeValidation, "projection scope is required", false)
	}
	queries, err := store.queries()
	if err != nil {
		return nil, err
	}
	rows, err := queries.PendingProjection(ctx, sqlc.PendingProjectionParams{ScopeID: scopeID, NextAttemptAt: sql.NullString{String: formatTimestamp(now), Valid: true}})
	if err != nil {
		return nil, domain.MapSQLiteError(err)
	}
	requests := make([]ports.ProjectionRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, ports.ProjectionRequest{ScopeID: row.ScopeID, RevisionWatermark: row.RevisionWatermark})
	}
	return requests, nil
}

func (store *ProjectionStore) AcknowledgeProjection(ctx context.Context, scopeID string, revisionWatermark int64) error {
	if scopeID == "" || revisionWatermark < 0 {
		return domain.NewError(domain.CodeValidation, "projection acknowledgement is invalid", false)
	}
	if _, err := store.databaseHandle(); err != nil {
		return err
	}
	err := store.database.withTx(ctx, func(tx *sql.Tx) error {
		queries := sqlc.New(tx)
		if err := queries.AcknowledgeProjection(ctx, sqlc.AcknowledgeProjectionParams{ScopeID: scopeID, RevisionWatermark: revisionWatermark}); err != nil {
			return domain.MapSQLiteError(err)
		}
		return domain.MapSQLiteError(queries.ResetProjectionState(ctx, sqlc.ResetProjectionStateParams{ScopeID: scopeID, SafeError: "", UpdatedAt: formatTimestamp(time.Now().UTC())}))
	})
	return err
}

func (store *ProjectionStore) RecordProjectionFailure(ctx context.Context, scopeID string, revisionWatermark int64, attemptCount int64, nextAttemptAt time.Time, safeError string) error {
	if scopeID == "" || revisionWatermark < 0 || attemptCount < 1 || nextAttemptAt.IsZero() {
		return domain.NewError(domain.CodeValidation, "projection failure metadata is invalid", false)
	}
	queries, err := store.queries()
	if err != nil {
		return err
	}
	err = queries.RecordProjectionFailure(ctx, sqlc.RecordProjectionFailureParams{
		ScopeID:           scopeID,
		RevisionWatermark: revisionWatermark,
		AttemptCount:      attemptCount,
		NextAttemptAt:     sql.NullString{String: formatTimestamp(nextAttemptAt), Valid: true},
		SafeError:         boundedProjectionError(safeError),
		CreatedAt:         formatTimestamp(time.Now().UTC()),
	})
	return domain.MapSQLiteError(err)
}

func (store *ProjectionStore) ProjectionFingerprint(ctx context.Context, scopeID string) (string, error) {
	if scopeID == "" {
		return "", domain.NewError(domain.CodeValidation, "projection scope is required", false)
	}
	queries, err := store.queries()
	if err != nil {
		return "", err
	}
	state, err := queries.ReadProjectionState(ctx, scopeID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", domain.MapSQLiteError(err)
	}
	return state.Fingerprint, nil
}

func (store *ProjectionStore) SetProjectionState(ctx context.Context, scopeID, status, fingerprint string, revisionWatermark int64) error {
	if scopeID == "" || revisionWatermark < 0 {
		return domain.NewError(domain.CodeValidation, "projection state is invalid", false)
	}
	switch status {
	case "pending", "ready", "drifted", "blocked", "rebuilding":
	default:
		return domain.NewError(domain.CodeValidation, "projection status is invalid", false)
	}
	queries, err := store.queries()
	if err != nil {
		return err
	}
	err = queries.UpsertProjectionState(ctx, sqlc.UpsertProjectionStateParams{ScopeID: scopeID, RevisionWatermark: revisionWatermark, Status: status, Fingerprint: fingerprint, UpdatedAt: formatTimestamp(time.Now().UTC())})
	return domain.MapSQLiteError(err)
}

// ProjectionReady reports only durable failure/drift states. A pending outbox
// row is normal between a successful SQLite mutation and the next explicit
// worker run; failed or drifted projections keep the daemon unready.
func (store *ProjectionStore) ProjectionReady(ctx context.Context) error {
	queries, err := store.queries()
	if err != nil {
		return err
	}
	count, err := queries.CountProjectionBlockers(ctx)
	if err != nil {
		return domain.MapSQLiteError(err)
	}
	if count != 0 {
		return domain.NewError(domain.CodeUnavailable, "projection requires recovery", false)
	}
	return nil
}

func (store *ProjectionStore) queries() (*sqlc.Queries, error) {
	if store == nil || store.database == nil || store.database.sql == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "SQLite projection store unavailable", true)
	}
	return sqlc.New(store.database.sql), nil
}

func (store *ProjectionStore) databaseHandle() (*sql.DB, error) {
	if store == nil || store.database == nil || store.database.sql == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "SQLite projection store unavailable", true)
	}
	return store.database.sql, nil
}

func projectionPath(directory, scopeID string) string {
	digest := sha256.Sum256([]byte(scopeID))
	return filepath.Join(directory, hex.EncodeToString(digest[:])+".md")
}

func boundedProjectionError(value string) string {
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

var _ projection.ScopeSource = (*ProjectionStore)(nil)
var _ projection.OutboxStore = (*ProjectionStore)(nil)
