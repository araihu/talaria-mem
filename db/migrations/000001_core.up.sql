CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    revision_watermark INTEGER NOT NULL DEFAULT 0 CHECK (revision_watermark >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- The run journal is created before the later maintenance migration so a
-- failed migration can record its exact partial version even when the
-- failing migration's own DDL transaction is rolled back.
CREATE TABLE migration_journal (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL DEFAULT '',
    target_version INTEGER NOT NULL CHECK (target_version >= 0),
    current_version INTEGER NOT NULL CHECK (current_version >= 0),
    failed_version INTEGER CHECK (failed_version IS NULL OR failed_version >= 0),
    completed_version INTEGER CHECK (completed_version IS NULL OR completed_version >= 0),
    -- managed_backups is introduced by migration 3. Keeping this journal
    -- independent avoids a forward migration ordering dependency while
    -- preserving the non-content backup identity for later reconciliation.
    backup_id TEXT,
    failure_stage TEXT NOT NULL DEFAULT '' CHECK (failure_stage IN ('', 'started', 'rollback_before_commit', 'applied_ddl_before_clean')),
    schema_fingerprint TEXT NOT NULL DEFAULT '',
    dirty INTEGER NOT NULL DEFAULT 0 CHECK (dirty IN (0, 1)),
    safe_error TEXT NOT NULL DEFAULT '',
    started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (length(run_id) <= 128 AND instr(run_id, char(0)) = 0 AND instr(run_id, char(10)) = 0 AND instr(run_id, char(13)) = 0),
    CHECK (length(schema_fingerprint) <= 64 AND instr(schema_fingerprint, char(0)) = 0 AND instr(schema_fingerprint, char(10)) = 0 AND instr(schema_fingerprint, char(13)) = 0),
    CHECK (length(safe_error) <= 512 AND instr(safe_error, char(0)) = 0 AND instr(safe_error, char(10)) = 0 AND instr(safe_error, char(13)) = 0)
) STRICT;

CREATE TABLE workspace_bindings (
    binding_key TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    binding_kind TEXT NOT NULL CHECK (binding_kind IN ('explicit', 'git_remote', 'root_fingerprint', 'absolute_path')),
    first_inference_warned INTEGER NOT NULL DEFAULT 0 CHECK (first_inference_warned IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE workspace_redirects (
    source_workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE RESTRICT,
    target_workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    CHECK (source_workspace_id <> target_workspace_id)
) STRICT;

CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE RESTRICT,
    user_global INTEGER NOT NULL DEFAULT 0 CHECK (user_global IN (0, 1)),
    kind TEXT NOT NULL CHECK (kind IN ('state', 'procedure', 'failure', 'standing_instruction')),
    trust TEXT NOT NULL CHECK (trust IN ('verified', 'unverified')),
    lifecycle TEXT NOT NULL CHECK (lifecycle IN ('active', 'quarantined', 'forgotten', 'purged')),
    current_revision_id TEXT,
    pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((user_global = 1 AND workspace_id IS NULL) OR (user_global = 0 AND workspace_id IS NOT NULL))
) STRICT;

CREATE TABLE memory_revisions (
    id TEXT PRIMARY KEY,
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL CHECK (revision_number > 0),
    kind TEXT NOT NULL CHECK (kind IN ('state', 'procedure', 'failure', 'standing_instruction')),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    tags_json TEXT NOT NULL,
    resolution_state TEXT,
    trust TEXT NOT NULL CHECK (trust IN ('verified', 'unverified')),
    lifecycle TEXT NOT NULL CHECK (lifecycle IN ('active', 'quarantined', 'forgotten', 'purged')),
    provenance_actor TEXT NOT NULL DEFAULT '',
    provenance_source TEXT NOT NULL DEFAULT '',
    provenance_labels_json TEXT NOT NULL DEFAULT '[]',
    source_locator TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE (memory_id, revision_number),
    CHECK (
        (kind = 'failure' AND resolution_state IS NOT NULL AND resolution_state IN ('open', 'resolved')) OR
        (kind <> 'failure' AND resolution_state IS NULL)
    )
) STRICT;

CREATE UNIQUE INDEX memory_revisions_memory_id_id
    ON memory_revisions(memory_id, id);

CREATE TRIGGER memories_current_revision_insert
BEFORE INSERT ON memories
WHEN NEW.current_revision_id IS NOT NULL
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM memory_revisions
        WHERE id = NEW.current_revision_id AND memory_id = NEW.id
    ) THEN RAISE(ABORT, 'invalid current revision') END;
END;

CREATE TRIGGER memories_current_revision_update
BEFORE UPDATE OF current_revision_id ON memories
WHEN NEW.current_revision_id IS NOT NULL
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1 FROM memory_revisions
        WHERE id = NEW.current_revision_id AND memory_id = NEW.id
    ) THEN RAISE(ABORT, 'invalid current revision') END;
END;

CREATE TABLE memory_aliases (
    alias_memory_id TEXT PRIMARY KEY,
    canonical_memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    CHECK (alias_memory_id <> canonical_memory_id)
) STRICT;

CREATE VIRTUAL TABLE memory_fts USING fts5(
    title,
    content,
    tags,
    memory_id UNINDEXED,
    tokenize='unicode61 remove_diacritics 2'
);

INSERT INTO memory_fts(memory_fts, rank) VALUES('secure-delete', 1);

CREATE TABLE outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scope_id TEXT NOT NULL,
    revision_watermark INTEGER NOT NULL CHECK (revision_watermark >= 0),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT,
    safe_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE (scope_id, revision_watermark)
) STRICT;

CREATE TABLE projection_state (
    scope_id TEXT PRIMARY KEY,
    revision_watermark INTEGER NOT NULL DEFAULT 0 CHECK (revision_watermark >= 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'drifted', 'blocked', 'rebuilding')),
    fingerprint TEXT NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT,
    safe_error TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL
) STRICT;
