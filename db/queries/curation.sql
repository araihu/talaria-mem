-- name: ReadCurationJobByDigestWatermark :one
SELECT id, workspace_id, reason, reason_flags, priority, session_digest,
       source_watermark, thread_locator_ciphertext, snapshot_ciphertext,
       state, attempt_count, next_attempt_at, expires_at, provider_name,
       safe_error_class, created_at, updated_at
FROM curation_jobs
WHERE session_digest = ? AND source_watermark = ?;

-- name: ReadCurationJobByID :one
SELECT id, workspace_id, reason, reason_flags, priority, session_digest,
       source_watermark, thread_locator_ciphertext, snapshot_ciphertext,
       state, attempt_count, next_attempt_at, expires_at, provider_name,
       safe_error_class, created_at, updated_at
FROM curation_jobs
WHERE id = ?;

-- name: InsertCurationJob :exec
INSERT INTO curation_jobs(
    id, workspace_id, reason, reason_flags, priority, session_digest,
    source_watermark, thread_locator_ciphertext, snapshot_ciphertext,
    state, attempt_count, next_attempt_at, expires_at, provider_name,
    safe_error_class, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateCurationJobCoalesced :exec
UPDATE curation_jobs
SET reason = ?,
    reason_flags = ?,
    priority = ?,
    thread_locator_ciphertext = ?,
    snapshot_ciphertext = ?,
    expires_at = ?,
    state = CASE WHEN state = 'retry_wait' THEN 'pending' ELSE state END,
    next_attempt_at = CASE WHEN state = 'retry_wait' THEN NULL ELSE next_attempt_at END,
    updated_at = ?
WHERE id = ?;

-- name: ReadClaimableCurationJob :one
SELECT id, workspace_id, reason, reason_flags, priority, session_digest,
       source_watermark, thread_locator_ciphertext, snapshot_ciphertext,
       state, attempt_count, next_attempt_at, expires_at, provider_name,
       safe_error_class, created_at, updated_at
FROM curation_jobs
WHERE state IN ('pending', 'retry_wait')
  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
  AND expires_at > ?
ORDER BY priority DESC, created_at ASC, id ASC
LIMIT 1;

-- name: ClaimCurationJob :execrows
UPDATE curation_jobs
SET state = 'running', attempt_count = attempt_count + 1,
    next_attempt_at = NULL, updated_at = ?
WHERE id = ? AND state IN ('pending', 'retry_wait')
  AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
  AND expires_at > ?;

-- name: RetryCurationJob :execrows
UPDATE curation_jobs
SET state = 'retry_wait', attempt_count = ?, next_attempt_at = ?,
    safe_error_class = ?, updated_at = ?
WHERE id = ? AND state = 'running';

-- name: FinishCurationJob :execrows
UPDATE curation_jobs
SET state = ?, thread_locator_ciphertext = X'', snapshot_ciphertext = X'',
    next_attempt_at = NULL, safe_error_class = ?, updated_at = ?
WHERE id = ? AND state = 'running';

-- name: PurgeExpiredCurationJobs :execrows
DELETE FROM curation_jobs
WHERE expires_at <= ? AND state <> 'running';

-- name: ReadCurationSessionCounter :one
SELECT session_digest, prompt_count, last_watermark, expires_at
FROM curation_session_counters
WHERE session_digest = ?;

-- name: IncrementCurationSessionCounter :exec
INSERT INTO curation_session_counters(session_digest, prompt_count, last_watermark, expires_at)
VALUES (?, 1, ?, ?)
ON CONFLICT(session_digest) DO UPDATE SET
    prompt_count = curation_session_counters.prompt_count + 1,
    last_watermark = max(curation_session_counters.last_watermark, excluded.last_watermark),
    expires_at = max(curation_session_counters.expires_at, excluded.expires_at);

-- name: DeleteCurationSessionCounter :exec
DELETE FROM curation_session_counters WHERE session_digest = ?;
