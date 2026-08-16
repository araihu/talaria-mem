-- name: ListProjectionMemoryIDs :many
SELECT memories.id
FROM memories
JOIN memory_revisions AS revisions ON revisions.id = memories.current_revision_id
WHERE ((? = 'global' AND memories.user_global = 1) OR memories.workspace_id = ?)
  AND memories.trust = 'verified'
  AND memories.lifecycle = 'active'
  AND revisions.trust = 'verified'
  AND revisions.lifecycle = 'active'
ORDER BY memories.id;

-- name: PendingProjection :many
SELECT scope_id, revision_watermark
FROM outbox
WHERE scope_id = ?
  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
ORDER BY revision_watermark, id;

-- name: AcknowledgeProjection :exec
DELETE FROM outbox
WHERE scope_id = ? AND revision_watermark <= ?;

-- name: ResetProjectionState :exec
UPDATE projection_state
SET attempt_count = 0, next_attempt_at = NULL, safe_error = ?, updated_at = ?
WHERE scope_id = ?;

-- name: RecordProjectionFailure :exec
INSERT INTO outbox(
    scope_id, revision_watermark, attempt_count, next_attempt_at, safe_error, created_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(scope_id, revision_watermark) DO UPDATE SET
    attempt_count = excluded.attempt_count,
    next_attempt_at = excluded.next_attempt_at,
    safe_error = excluded.safe_error;

-- name: ReadProjectionState :one
SELECT scope_id, revision_watermark, status, fingerprint, attempt_count,
    next_attempt_at, safe_error, updated_at
FROM projection_state
WHERE scope_id = ?;

-- name: UpsertProjectionState :exec
INSERT INTO projection_state(
    scope_id, revision_watermark, status, fingerprint, updated_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(scope_id) DO UPDATE SET
    revision_watermark = excluded.revision_watermark,
    status = excluded.status,
    fingerprint = excluded.fingerprint,
    updated_at = excluded.updated_at;

-- name: CountProjectionBlockers :one
SELECT count(*)
FROM projection_state
WHERE status IN ('drifted', 'blocked', 'rebuilding');
