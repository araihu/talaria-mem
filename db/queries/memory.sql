-- name: CreateMemory :exec
INSERT INTO memories (
    id, workspace_id, user_global, kind, trust, lifecycle, current_revision_id,
    generated_fingerprint, pinned, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateMemoryRevision :exec
INSERT INTO memory_revisions (
    id, memory_id, revision_number, kind, title, content, tags_json,
    resolution_state, trust, lifecycle, provenance_actor, provenance_source,
    provenance_labels_json, source_locator, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: MoveCurrentRevision :execrows
UPDATE memories
SET current_revision_id = ?, kind = ?, trust = ?, lifecycle = ?, updated_at = ?
WHERE id = ? AND current_revision_id = ?;

-- name: MoveInitialCurrentRevision :execrows
UPDATE memories
SET current_revision_id = ?, kind = ?, trust = ?, lifecycle = ?, updated_at = ?
WHERE id = ? AND current_revision_id IS NULL;

-- name: DeleteFTSRow :exec
DELETE FROM memory_fts WHERE memory_id = ?;

-- name: DeleteAllFTSRows :exec
DELETE FROM memory_fts;

-- name: InsertEligibleFTSRow :exec
INSERT INTO memory_fts(title, content, tags, memory_id)
SELECT revisions.title, revisions.content, revisions.tags_json, memories.id
FROM memories
JOIN memory_revisions AS revisions ON revisions.id = memories.current_revision_id
WHERE memories.id = ?
  AND memories.trust IN ('verified', 'generated')
  AND memories.lifecycle = 'active'
  AND revisions.trust IN ('verified', 'generated')
  AND revisions.lifecycle = 'active';

-- name: RebuildEligibleFTSRows :exec
INSERT INTO memory_fts(title, content, tags, memory_id)
SELECT revisions.title, revisions.content, revisions.tags_json, memories.id
FROM memories
JOIN memory_revisions AS revisions ON revisions.id = memories.current_revision_id
WHERE memories.trust IN ('verified', 'generated')
  AND memories.lifecycle = 'active'
  AND revisions.trust IN ('verified', 'generated')
  AND revisions.lifecycle = 'active'
ORDER BY memories.id;

-- name: AppendOutbox :exec
INSERT INTO outbox(scope_id, revision_watermark, created_at)
VALUES (?, ?, ?)
ON CONFLICT(scope_id, revision_watermark) DO NOTHING;

-- name: ReadCurrent :one
SELECT
    memories.id, memories.workspace_id, memories.user_global, memories.kind,
    memories.trust, memories.lifecycle, memories.current_revision_id,
    memories.generated_fingerprint, memories.pinned, memories.created_at, memories.updated_at,
    revisions.id, revisions.revision_number, revisions.kind, revisions.title,
    revisions.content, revisions.tags_json, revisions.resolution_state,
    revisions.trust, revisions.lifecycle, revisions.provenance_actor,
    revisions.provenance_source, revisions.provenance_labels_json,
    revisions.source_locator, revisions.created_at
FROM memories
JOIN memory_revisions AS revisions ON revisions.id = memories.current_revision_id
WHERE memories.id = ?;

-- name: ActiveGeneratedFingerprintExists :one
SELECT EXISTS(
    SELECT 1 FROM memories
    WHERE workspace_id = ?
      AND generated_fingerprint = ?
      AND trust = 'generated'
      AND lifecycle = 'active'
);
