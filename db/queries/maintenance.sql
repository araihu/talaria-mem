-- name: ReadActivationJournal :one
SELECT id, version, activation_epoch, active_generation, candidate_generation, phase,
    revision_watermark, candidate_rule_fingerprint, last_processed_id,
    live_mutation_started, quarantined_count, fts_removed_count,
    outbox_added_count, projected_count, resume_cursor, safe_error,
    comparative_verified, rescan_verified, mutation_verified,
    projection_verified, readiness_verified, candidate_discard_reverified,
    historical_live_mutation_started, historical_quarantined_count,
    historical_fts_removed_count, historical_outbox_added_count,
    historical_projected_count, updated_at
FROM rule_activation_journal WHERE id = 1;

-- name: StoreActivationJournal :execrows
UPDATE rule_activation_journal
SET version = version + 1,
    activation_epoch = ?, active_generation = ?, candidate_generation = ?, phase = ?,
    revision_watermark = ?, candidate_rule_fingerprint = ?,
    last_processed_id = ?, live_mutation_started = ?, quarantined_count = ?,
    fts_removed_count = ?, outbox_added_count = ?, projected_count = ?,
    resume_cursor = ?, safe_error = ?, comparative_verified = ?,
    rescan_verified = ?, mutation_verified = ?, projection_verified = ?,
    readiness_verified = ?, candidate_discard_reverified = ?,
    historical_live_mutation_started = ?, historical_quarantined_count = ?,
    historical_fts_removed_count = ?, historical_outbox_added_count = ?,
    historical_projected_count = ?, updated_at = ?
WHERE id = 1 AND version = ?;

-- name: InsertPurgeOperation :exec
INSERT INTO purge_operations(
    id, operation_identity, operation_kind, memory_id, workspace_id, expected_revision_id, phase,
    receipt_digest, receipt_consumed, receipt_claim, inventory_watermark, resume_cursor, phase_cursor,
    safe_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
