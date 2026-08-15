-- name: CreateWorkspace :exec
INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: ReadWorkspace :one
SELECT id, name, revision_watermark, created_at, updated_at
FROM workspaces WHERE id = ?;

-- name: UpsertWorkspaceBinding :exec
INSERT INTO workspace_bindings(
    binding_key, workspace_id, binding_kind, first_inference_warned, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(binding_key) DO UPDATE SET
    workspace_id = excluded.workspace_id,
    binding_kind = excluded.binding_kind,
    first_inference_warned = excluded.first_inference_warned,
    updated_at = excluded.updated_at;
