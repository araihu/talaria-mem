-- name: CreateWorkspace :exec
INSERT INTO workspaces(id, name, revision_watermark, created_at, updated_at)
VALUES (?, ?, ?, ?, ?);

-- name: ReadWorkspace :one
SELECT id, name, revision_watermark, created_at, updated_at
FROM workspaces WHERE id = ?;

-- name: ReadWorkspaceByIDOrName :one
SELECT id, name, revision_watermark, created_at, updated_at
FROM workspaces
WHERE id = ? OR name = ?
ORDER BY id
LIMIT 1;

-- name: ListWorkspaces :many
SELECT id, name, revision_watermark, created_at, updated_at
FROM workspaces
ORDER BY name, id;

-- name: ReadWorkspaceBinding :one
SELECT binding_key, workspace_id, binding_kind, first_inference_warned, created_at, updated_at
FROM workspace_bindings WHERE binding_key = ?;

-- name: UpsertWorkspaceBinding :exec
INSERT INTO workspace_bindings(
    binding_key, workspace_id, binding_kind, first_inference_warned, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(binding_key) DO UPDATE SET
    workspace_id = excluded.workspace_id,
    binding_kind = excluded.binding_kind,
    first_inference_warned = CASE
        WHEN workspace_bindings.first_inference_warned = 1 OR excluded.first_inference_warned = 1 THEN 1
        ELSE 0
    END,
    created_at = workspace_bindings.created_at,
    updated_at = excluded.updated_at;
