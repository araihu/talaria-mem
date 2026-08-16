CREATE TABLE purge_operations (
    id TEXT PRIMARY KEY,
    operation_identity TEXT NOT NULL DEFAULT '',
    operation_kind TEXT NOT NULL CHECK (operation_kind IN ('memory_purge', 'backup_reconcile')),
    memory_id TEXT,
    workspace_id TEXT,
    expected_revision_id TEXT,
    phase TEXT NOT NULL CHECK (phase IN (
        'reserved', 'purge_pending', 'pre_effect', 'effect_applied', 'post_effect', 'complete', 'failed',
        'quarantine_renamed', 'file_fsynced', 'deleted', 'inventory_rebuilt', 'receipt_complete'
    )),
    receipt_digest BLOB NOT NULL,
    receipt_consumed INTEGER NOT NULL DEFAULT 0 CHECK (receipt_consumed IN (0, 1)),
    receipt_claim TEXT NOT NULL DEFAULT '',
    inventory_watermark INTEGER NOT NULL DEFAULT 0 CHECK (inventory_watermark >= 0),
    resume_cursor TEXT NOT NULL DEFAULT '',
    phase_cursor TEXT NOT NULL DEFAULT '',
    safe_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT,
    CHECK (
        (operation_kind = 'backup_reconcile' AND phase IN (
            'pre_effect', 'quarantine_renamed', 'file_fsynced', 'deleted',
            'inventory_rebuilt', 'receipt_complete', 'failed'
        )) OR
        (operation_kind = 'memory_purge' AND phase IN (
            'reserved', 'purge_pending', 'pre_effect', 'effect_applied', 'post_effect', 'complete', 'failed'
        ))
    ),
    CHECK (length(operation_identity) > 0 AND length(operation_identity) <= 256 AND instr(operation_identity, char(0)) = 0 AND instr(operation_identity, char(10)) = 0 AND instr(operation_identity, char(13)) = 0),
    CHECK (length(receipt_claim) <= 256 AND instr(receipt_claim, char(0)) = 0 AND instr(receipt_claim, char(10)) = 0 AND instr(receipt_claim, char(13)) = 0),
    CHECK (length(phase_cursor) <= 256 AND instr(phase_cursor, char(0)) = 0 AND instr(phase_cursor, char(10)) = 0 AND instr(phase_cursor, char(13)) = 0),
    CHECK (length(safe_error) <= 512 AND instr(safe_error, char(0)) = 0 AND instr(safe_error, char(10)) = 0 AND instr(safe_error, char(13)) = 0)
) STRICT;

CREATE TABLE managed_backups (
    id TEXT PRIMARY KEY,
    path TEXT NOT NULL UNIQUE,
    sha256 TEXT NOT NULL,
    mode INTEGER NOT NULL,
    creation_cause TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version >= 0),
    database_revision_watermark INTEGER NOT NULL CHECK (database_revision_watermark >= 0),
    lifecycle TEXT NOT NULL CHECK (lifecycle IN ('pending', 'complete', 'cleanup_candidate', 'quarantined', 'deleted')),
    owner_id TEXT NOT NULL,
    key_version INTEGER NOT NULL CHECK (key_version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE rule_activation_journal (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    activation_epoch TEXT NOT NULL,
    active_generation TEXT NOT NULL,
    candidate_generation TEXT,
    phase TEXT NOT NULL CHECK (phase IN (
        'pending', 'quiesced', 'rescanning', 'quarantining', 'projection_rebuild',
        'active', 'candidate_discarded', 'live_mutation_started', 'failed', 'rollback'
    )),
    revision_watermark INTEGER NOT NULL CHECK (revision_watermark >= 0),
    candidate_rule_fingerprint TEXT NOT NULL DEFAULT '',
    last_processed_id TEXT NOT NULL DEFAULT '',
    live_mutation_started INTEGER NOT NULL DEFAULT 0 CHECK (live_mutation_started IN (0, 1)),
    quarantined_count INTEGER NOT NULL DEFAULT 0 CHECK (quarantined_count >= 0),
    fts_removed_count INTEGER NOT NULL DEFAULT 0 CHECK (fts_removed_count >= 0),
    outbox_added_count INTEGER NOT NULL DEFAULT 0 CHECK (outbox_added_count >= 0),
    projected_count INTEGER NOT NULL DEFAULT 0 CHECK (projected_count >= 0),
    resume_cursor TEXT NOT NULL DEFAULT '',
    safe_error TEXT NOT NULL DEFAULT '',
    comparative_verified INTEGER NOT NULL DEFAULT 0 CHECK (comparative_verified IN (0, 1)),
    rescan_verified INTEGER NOT NULL DEFAULT 0 CHECK (rescan_verified IN (0, 1)),
    mutation_verified INTEGER NOT NULL DEFAULT 0 CHECK (mutation_verified IN (0, 1)),
    projection_verified INTEGER NOT NULL DEFAULT 0 CHECK (projection_verified IN (0, 1)),
    readiness_verified INTEGER NOT NULL DEFAULT 0 CHECK (readiness_verified IN (0, 1)),
    candidate_discard_reverified INTEGER NOT NULL DEFAULT 0 CHECK (candidate_discard_reverified IN (0, 1)),
    historical_live_mutation_started INTEGER NOT NULL DEFAULT 0 CHECK (historical_live_mutation_started IN (0, 1)),
    historical_quarantined_count INTEGER NOT NULL DEFAULT 0 CHECK (historical_quarantined_count >= 0),
    historical_fts_removed_count INTEGER NOT NULL DEFAULT 0 CHECK (historical_fts_removed_count >= 0),
    historical_outbox_added_count INTEGER NOT NULL DEFAULT 0 CHECK (historical_outbox_added_count >= 0),
    historical_projected_count INTEGER NOT NULL DEFAULT 0 CHECK (historical_projected_count >= 0),
    updated_at TEXT NOT NULL,
    CHECK (NOT (live_mutation_started = 1 AND phase IN ('rollback', 'candidate_discarded'))),
    CHECK (length(active_generation) <= 128 AND instr(active_generation, char(0)) = 0 AND instr(active_generation, char(10)) = 0 AND instr(active_generation, char(13)) = 0),
    CHECK (length(activation_epoch) > 0 AND length(activation_epoch) <= 128 AND instr(activation_epoch, char(0)) = 0 AND instr(activation_epoch, char(10)) = 0 AND instr(activation_epoch, char(13)) = 0),
    CHECK (length(candidate_generation) <= 128 AND instr(candidate_generation, char(0)) = 0 AND instr(candidate_generation, char(10)) = 0 AND instr(candidate_generation, char(13)) = 0),
    CHECK (length(candidate_rule_fingerprint) <= 128 AND instr(candidate_rule_fingerprint, char(0)) = 0 AND instr(candidate_rule_fingerprint, char(10)) = 0 AND instr(candidate_rule_fingerprint, char(13)) = 0),
    CHECK (length(last_processed_id) <= 256 AND instr(last_processed_id, char(0)) = 0 AND instr(last_processed_id, char(10)) = 0 AND instr(last_processed_id, char(13)) = 0),
    CHECK (length(resume_cursor) <= 256 AND instr(resume_cursor, char(0)) = 0 AND instr(resume_cursor, char(10)) = 0 AND instr(resume_cursor, char(13)) = 0),
    CHECK (length(safe_error) <= 512 AND instr(safe_error, char(0)) = 0 AND instr(safe_error, char(10)) = 0 AND instr(safe_error, char(13)) = 0)
) STRICT;

INSERT INTO rule_activation_journal(
    id, version, activation_epoch, active_generation, candidate_generation, phase,
    revision_watermark, candidate_rule_fingerprint, last_processed_id,
    live_mutation_started, quarantined_count, fts_removed_count,
    outbox_added_count, projected_count, resume_cursor, safe_error,
    comparative_verified, rescan_verified, mutation_verified,
    projection_verified, readiness_verified, candidate_discard_reverified,
    historical_live_mutation_started, historical_quarantined_count,
    historical_fts_removed_count, historical_outbox_added_count,
    historical_projected_count, updated_at
) SELECT 1, 1, 'activation-epoch-1', 'betterleaks-v1.7.4-rules-09b7a5a71be10c6a571cf8dee1075af46be672600657e7b596559093834e57e8', NULL, 'active',
    0, '', '', 0, 0, 0, 0, 0, '', '', 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE NOT EXISTS (SELECT 1 FROM rule_activation_journal WHERE id = 1);

-- Per-epoch audit is append-preserving: the current journal may reset its
-- cycle-local counters for a later upgrade, but this table retains every
-- candidate identity and the highest observed counters for reconstruction.
CREATE TABLE activation_epoch_audit (
    activation_epoch TEXT PRIMARY KEY,
    active_generation TEXT NOT NULL,
    candidate_generation TEXT NOT NULL DEFAULT '',
    candidate_rule_fingerprint TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL,
    revision_watermark INTEGER NOT NULL CHECK (revision_watermark >= 0),
    live_mutation_started INTEGER NOT NULL CHECK (live_mutation_started IN (0, 1)),
    quarantined_count INTEGER NOT NULL DEFAULT 0 CHECK (quarantined_count >= 0),
    fts_removed_count INTEGER NOT NULL DEFAULT 0 CHECK (fts_removed_count >= 0),
    outbox_added_count INTEGER NOT NULL DEFAULT 0 CHECK (outbox_added_count >= 0),
    projected_count INTEGER NOT NULL DEFAULT 0 CHECK (projected_count >= 0),
    completed INTEGER NOT NULL DEFAULT 0 CHECK (completed IN (0, 1)),
    updated_at TEXT NOT NULL,
    CHECK (length(activation_epoch) > 0 AND length(activation_epoch) <= 128 AND instr(activation_epoch, char(0)) = 0 AND instr(activation_epoch, char(10)) = 0 AND instr(activation_epoch, char(13)) = 0),
    CHECK (length(active_generation) <= 128 AND instr(active_generation, char(0)) = 0 AND instr(active_generation, char(10)) = 0 AND instr(active_generation, char(13)) = 0),
    CHECK (length(candidate_generation) <= 128 AND instr(candidate_generation, char(0)) = 0 AND instr(candidate_generation, char(10)) = 0 AND instr(candidate_generation, char(13)) = 0),
    CHECK (length(candidate_rule_fingerprint) <= 128 AND instr(candidate_rule_fingerprint, char(0)) = 0 AND instr(candidate_rule_fingerprint, char(10)) = 0 AND instr(candidate_rule_fingerprint, char(13)) = 0)
) STRICT;

INSERT INTO activation_epoch_audit(
    activation_epoch, active_generation, phase, revision_watermark,
    live_mutation_started, updated_at
) SELECT activation_epoch, active_generation, phase, revision_watermark,
    live_mutation_started, updated_at
FROM rule_activation_journal
WHERE id = 1;
