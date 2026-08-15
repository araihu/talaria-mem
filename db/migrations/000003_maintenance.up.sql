CREATE TABLE purge_operations (
    id TEXT PRIMARY KEY,
    operation_kind TEXT NOT NULL CHECK (operation_kind IN ('memory_purge', 'backup_reconcile')),
    memory_id TEXT,
    workspace_id TEXT,
    expected_revision_id TEXT,
    phase TEXT NOT NULL CHECK (phase IN ('reserved', 'purge_pending', 'pre_effect', 'effect_applied', 'post_effect', 'complete', 'failed')),
    receipt_digest BLOB NOT NULL,
    receipt_consumed INTEGER NOT NULL DEFAULT 0 CHECK (receipt_consumed IN (0, 1)),
    inventory_watermark INTEGER NOT NULL DEFAULT 0 CHECK (inventory_watermark >= 0),
    resume_cursor TEXT NOT NULL DEFAULT '',
    safe_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT
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

CREATE TABLE migration_journal (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    target_version INTEGER NOT NULL CHECK (target_version >= 0),
    current_version INTEGER NOT NULL CHECK (current_version >= 0),
    failed_version INTEGER CHECK (failed_version IS NULL OR failed_version >= 0),
    completed_version INTEGER CHECK (completed_version IS NULL OR completed_version >= 0),
    backup_id TEXT REFERENCES managed_backups(id) ON DELETE RESTRICT,
    safe_error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE rule_activation_journal (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
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
    updated_at TEXT NOT NULL,
    CHECK (NOT (live_mutation_started = 1 AND phase IN ('rollback', 'candidate_discarded')))
) STRICT;
