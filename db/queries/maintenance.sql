-- name: ReadActivationJournal :one
SELECT id, version, active_generation, candidate_generation, phase,
    revision_watermark, candidate_rule_fingerprint, last_processed_id,
    live_mutation_started, quarantined_count, fts_removed_count,
    outbox_added_count, projected_count, resume_cursor, safe_error, updated_at
FROM rule_activation_journal WHERE id = 1;

-- name: StoreActivationJournal :execrows
UPDATE rule_activation_journal
SET version = version + 1,
    active_generation = ?, candidate_generation = ?, phase = ?,
    revision_watermark = ?, candidate_rule_fingerprint = ?,
    last_processed_id = ?, live_mutation_started = ?, quarantined_count = ?,
    fts_removed_count = ?, outbox_added_count = ?, projected_count = ?,
    resume_cursor = ?, safe_error = ?, updated_at = ?
WHERE id = 1 AND version = ?;

-- name: InsertPurgeOperation :exec
INSERT INTO purge_operations(
    id, operation_kind, memory_id, workspace_id, expected_revision_id, phase,
    receipt_digest, receipt_consumed, inventory_watermark, resume_cursor,
    safe_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
