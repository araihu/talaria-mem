package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/adapters/sqlite/sqlc"
	"github.com/guilhermecastro/talaria-mem/internal/domain"
	workspacepkg "github.com/guilhermecastro/talaria-mem/internal/workspace"
)

// WorkspaceStore is the durable workspace/binding adapter. Workspace
// identity is canonical in SQLite; the in-memory store remains useful for
// isolated package tests but must not be used by the runtime composition.
type WorkspaceStore struct {
	database *DB
}

func NewWorkspaceStore(database *DB) *WorkspaceStore {
	return &WorkspaceStore{database: database}
}

func (store *WorkspaceStore) queries() (*sqlc.Queries, error) {
	if store == nil || store.database == nil || store.database.sql == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "SQLite workspace store unavailable", true)
	}
	return sqlc.New(store.database.sql), nil
}

func (store *WorkspaceStore) ReadWorkspace(ctx context.Context, idOrName string) (domain.Workspace, bool, error) {
	if idOrName == "" {
		return domain.Workspace{}, false, domain.NewError(domain.CodeValidation, "workspace identity is required", false)
	}
	queries, err := store.queries()
	if err != nil {
		return domain.Workspace{}, false, err
	}
	row, err := queries.ReadWorkspaceByIDOrName(ctx, sqlc.ReadWorkspaceByIDOrNameParams{ID: idOrName, Name: idOrName})
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.Workspace{}, false, nil
		}
		return domain.Workspace{}, false, domain.MapSQLiteError(err)
	}
	workspace, err := decodeWorkspace(row)
	if err != nil {
		return domain.Workspace{}, false, err
	}
	return workspace, true, nil
}

func (store *WorkspaceStore) ListWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	queries, err := store.queries()
	if err != nil {
		return nil, err
	}
	rows, err := queries.ListWorkspaces(ctx)
	if err != nil {
		return nil, domain.MapSQLiteError(err)
	}
	items := make([]domain.Workspace, 0, len(rows))
	for _, row := range rows {
		item, err := decodeWorkspace(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (store *WorkspaceStore) CreateWorkspace(ctx context.Context, workspace domain.Workspace) error {
	if workspace.ID == "" || workspace.Name == "" {
		return domain.NewError(domain.CodeValidation, "workspace identity is required", false)
	}
	if workspace.RevisionWatermark < 0 {
		return domain.NewError(domain.CodeValidation, "workspace revision watermark is invalid", false)
	}
	now := time.Now().UTC()
	if workspace.CreatedAt.IsZero() {
		workspace.CreatedAt = now
	}
	if workspace.UpdatedAt.IsZero() {
		workspace.UpdatedAt = workspace.CreatedAt
	}
	queries, err := store.queries()
	if err != nil {
		return err
	}
	err = queries.CreateWorkspace(ctx, sqlc.CreateWorkspaceParams{
		ID:                workspace.ID,
		Name:              workspace.Name,
		RevisionWatermark: workspace.RevisionWatermark,
		CreatedAt:         formatTimestamp(workspace.CreatedAt),
		UpdatedAt:         formatTimestamp(workspace.UpdatedAt),
	})
	return domain.MapSQLiteError(err)
}

func (store *WorkspaceStore) ReadBinding(ctx context.Context, key string) (workspacepkg.Binding, bool, error) {
	if key == "" {
		return workspacepkg.Binding{}, false, domain.NewError(domain.CodeValidation, "binding key is required", false)
	}
	queries, err := store.queries()
	if err != nil {
		return workspacepkg.Binding{}, false, err
	}
	row, err := queries.ReadWorkspaceBinding(ctx, key)
	if err != nil {
		if err == sql.ErrNoRows {
			return workspacepkg.Binding{}, false, nil
		}
		return workspacepkg.Binding{}, false, domain.MapSQLiteError(err)
	}
	binding, err := decodeBinding(row)
	if err != nil {
		return workspacepkg.Binding{}, false, err
	}
	return binding, true, nil
}

func (store *WorkspaceStore) SaveBinding(ctx context.Context, binding workspacepkg.Binding) error {
	if binding.Key == "" || binding.WorkspaceID == "" || !binding.Kind.Valid() {
		return domain.NewError(domain.CodeValidation, "invalid workspace binding", false)
	}
	now := time.Now().UTC()
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	if binding.UpdatedAt.IsZero() {
		binding.UpdatedAt = binding.CreatedAt
	}
	queries, err := store.queries()
	if err != nil {
		return err
	}
	err = queries.UpsertWorkspaceBinding(ctx, sqlc.UpsertWorkspaceBindingParams{
		BindingKey:           binding.Key,
		WorkspaceID:          binding.WorkspaceID,
		BindingKind:          string(binding.Kind),
		FirstInferenceWarned: boolInt(binding.FirstInferenceWarned),
		CreatedAt:            formatTimestamp(binding.CreatedAt),
		UpdatedAt:            formatTimestamp(binding.UpdatedAt),
	})
	return domain.MapSQLiteError(err)
}

func (store *WorkspaceStore) ReadRedirect(ctx context.Context, sourceWorkspaceID string) (string, bool, error) {
	if sourceWorkspaceID == "" {
		return "", false, domain.NewError(domain.CodeValidation, "source workspace is required", false)
	}
	database, err := store.databaseHandle()
	if err != nil {
		return "", false, err
	}
	var target string
	err = database.QueryRowContext(ctx, `
SELECT target_workspace_id
FROM workspace_redirects
WHERE source_workspace_id = ?`, sourceWorkspaceID).Scan(&target)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, domain.MapSQLiteError(err)
	}
	return target, true, nil
}

func (store *WorkspaceStore) ListMemories(ctx context.Context, workspaceID string) ([]workspacepkg.MemoryRecord, error) {
	if workspaceID == "" {
		return nil, domain.NewError(domain.CodeValidation, "workspace identity is required", false)
	}
	database, err := store.databaseHandle()
	if err != nil {
		return nil, err
	}
	rows, err := database.QueryContext(ctx, `
SELECT id
FROM memories
WHERE workspace_id = ?
ORDER BY id`, workspaceID)
	if err != nil {
		return nil, domain.MapSQLiteError(err)
	}
	defer rows.Close()
	repository := NewRepository(store.database)
	items := make([]workspacepkg.MemoryRecord, 0)
	for rows.Next() {
		var memoryID string
		if err := rows.Scan(&memoryID); err != nil {
			return nil, domain.MapSQLiteError(err)
		}
		memory, revision, err := repository.ReadCurrent(ctx, memoryID)
		if err != nil {
			return nil, err
		}
		items = append(items, workspacepkg.MemoryRecord{Memory: memory, Revision: revision, Revisions: []domain.MemoryRevision{revision}})
	}
	if err := rows.Err(); err != nil {
		return nil, domain.MapSQLiteError(err)
	}
	return items, nil
}

func (store *WorkspaceStore) ApplyMerge(ctx context.Context, plan workspacepkg.MergePlan, receipt workspacepkg.MergeReceipt) error {
	if plan.SourceWorkspaceID == "" || plan.TargetWorkspaceID == "" || plan.SourceWorkspaceID == plan.TargetWorkspaceID {
		return domain.NewError(domain.CodeValidation, "distinct source and target workspaces are required", false)
	}
	if _, err := store.databaseHandle(); err != nil {
		return err
	}
	return store.database.withTx(ctx, func(tx *sql.Tx) error {
		var existingTarget string
		err := tx.QueryRowContext(ctx, `
SELECT target_workspace_id
FROM workspace_redirects
WHERE source_workspace_id = ?`, plan.SourceWorkspaceID).Scan(&existingTarget)
		switch {
		case err == nil && existingTarget == plan.TargetWorkspaceID:
			return nil
		case err == nil:
			return domain.NewError(domain.CodeValidation, "workspace redirect cycle", false)
		case err != sql.ErrNoRows:
			return domain.MapSQLiteError(err)
		}
		var targetRedirect string
		err = tx.QueryRowContext(ctx, `
SELECT target_workspace_id
FROM workspace_redirects
WHERE source_workspace_id = ?`, plan.TargetWorkspaceID).Scan(&targetRedirect)
		if err == nil {
			return domain.NewError(domain.CodeValidation, "target workspace already redirects", false)
		}
		if err != sql.ErrNoRows {
			return domain.MapSQLiteError(err)
		}

		now := formatTimestamp(time.Now().UTC())
		for alias, canonical := range plan.Aliases {
			var existingCanonical string
			err := tx.QueryRowContext(ctx, `
SELECT canonical_memory_id
FROM memory_aliases
WHERE alias_memory_id = ?`, alias).Scan(&existingCanonical)
			if err == nil {
				if existingCanonical != canonical {
					return domain.NewError(domain.CodeRevisionConflict, "memory alias already points elsewhere", false)
				}
				continue
			}
			if err != sql.ErrNoRows {
				return domain.MapSQLiteError(err)
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO memory_aliases(alias_memory_id, canonical_memory_id, created_at)
VALUES (?, ?, ?)`, alias, canonical, now); err != nil {
				return domain.MapSQLiteError(err)
			}
		}
		for _, memoryID := range plan.MemoryIDs {
			result, err := tx.ExecContext(ctx, `
UPDATE memories
SET workspace_id = ?, updated_at = ?
WHERE id = ? AND workspace_id = ?`, plan.TargetWorkspaceID, now, memoryID, plan.SourceWorkspaceID)
			if err != nil {
				return domain.MapSQLiteError(err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return domain.MapSQLiteError(err)
			}
			if affected != 1 {
				return domain.NewError(domain.CodeRevisionConflict, "workspace merge memory is stale", false)
			}
		}
		for _, workspaceID := range []string{plan.SourceWorkspaceID, plan.TargetWorkspaceID} {
			result, err := tx.ExecContext(ctx, `
UPDATE workspaces
SET revision_watermark = revision_watermark + 1, updated_at = ?
WHERE id = ? AND revision_watermark = ?`, now, workspaceID, watermarkFor(workspaceID, plan))
			if err != nil {
				return domain.MapSQLiteError(err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return domain.MapSQLiteError(err)
			}
			if affected != 1 {
				return domain.NewError(domain.CodeRevisionConflict, "workspace merge watermark is stale", false)
			}
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO workspace_redirects(source_workspace_id, target_workspace_id, created_at)
VALUES (?, ?, ?)`, plan.SourceWorkspaceID, plan.TargetWorkspaceID, now)
		return domain.MapSQLiteError(err)
	})
}

func (store *WorkspaceStore) databaseHandle() (*sql.DB, error) {
	if store == nil || store.database == nil || store.database.sql == nil {
		return nil, domain.NewError(domain.CodeUnavailable, "SQLite workspace store unavailable", true)
	}
	return store.database.sql, nil
}

func watermarkFor(workspaceID string, plan workspacepkg.MergePlan) int64 {
	if workspaceID == plan.SourceWorkspaceID {
		return plan.SourceWatermark
	}
	return plan.TargetWatermark
}

func decodeWorkspace(row sqlc.Workspace) (domain.Workspace, error) {
	createdAt, err := parseTimestamp(row.CreatedAt)
	if err != nil {
		return domain.Workspace{}, err
	}
	updatedAt, err := parseTimestamp(row.UpdatedAt)
	if err != nil {
		return domain.Workspace{}, err
	}
	return domain.Workspace{ID: row.ID, Name: row.Name, RevisionWatermark: row.RevisionWatermark, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func decodeBinding(row sqlc.WorkspaceBinding) (workspacepkg.Binding, error) {
	createdAt, err := parseTimestamp(row.CreatedAt)
	if err != nil {
		return workspacepkg.Binding{}, err
	}
	updatedAt, err := parseTimestamp(row.UpdatedAt)
	if err != nil {
		return workspacepkg.Binding{}, err
	}
	kind := workspacepkg.BindingKind(row.BindingKind)
	if !kind.Valid() {
		return workspacepkg.Binding{}, domain.NewError(domain.CodeUnavailable, "stored workspace binding kind is invalid", false)
	}
	return workspacepkg.Binding{Key: row.BindingKey, WorkspaceID: row.WorkspaceID, Kind: kind, FirstInferenceWarned: row.FirstInferenceWarned == 1, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

var _ workspacepkg.Store = (*WorkspaceStore)(nil)
var _ workspacepkg.MergeStore = (*WorkspaceStore)(nil)
