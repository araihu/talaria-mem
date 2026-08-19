DROP TRIGGER IF EXISTS memories_current_revision_insert;
DROP TRIGGER IF EXISTS memories_current_revision_update;
DROP INDEX IF EXISTS memory_revisions_memory_id_id;

-- Rebuild every table that carries a foreign key to the core memory tables.
-- SQLite rewrites those references when the parent tables are renamed; keep
-- the old graph intact until the replacement graph exists.
ALTER TABLE memory_aliases RENAME TO memory_aliases_v3;
ALTER TABLE usage_daily RENAME TO usage_daily_v3;
ALTER TABLE usage_lifetime RENAME TO usage_lifetime_v3;
ALTER TABLE usage_session_hits RENAME TO usage_session_hits_v3;
ALTER TABLE skill_promotions RENAME TO skill_promotions_v3;
ALTER TABLE memory_revisions RENAME TO memory_revisions_v3;
ALTER TABLE memories RENAME TO memories_v3;

CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE RESTRICT,
    user_global INTEGER NOT NULL DEFAULT 0 CHECK (user_global IN (0, 1)),
    kind TEXT NOT NULL CHECK (kind IN ('state', 'procedure', 'failure', 'standing_instruction')),
    trust TEXT NOT NULL CHECK (trust IN ('verified', 'generated', 'unverified')),
    lifecycle TEXT NOT NULL CHECK (lifecycle IN ('active', 'quarantined', 'forgotten', 'purged')),
    current_revision_id TEXT,
    generated_fingerprint TEXT NOT NULL DEFAULT '',
    pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((user_global = 1 AND workspace_id IS NULL) OR (user_global = 0 AND workspace_id IS NOT NULL)),
    CHECK (NOT (trust = 'generated' AND user_global = 1)),
    CHECK ((trust = 'generated' AND length(generated_fingerprint) > 0) OR (trust <> 'generated' AND generated_fingerprint = '')),
    CHECK (length(generated_fingerprint) <= 128 AND instr(generated_fingerprint, char(0)) = 0 AND instr(generated_fingerprint, char(10)) = 0 AND instr(generated_fingerprint, char(13)) = 0)
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
    trust TEXT NOT NULL CHECK (trust IN ('verified', 'generated', 'unverified')),
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
    ),
    CHECK (NOT (trust = 'generated' AND kind = 'standing_instruction'))
) STRICT;

CREATE UNIQUE INDEX memory_revisions_memory_id_id
    ON memory_revisions(memory_id, id);

INSERT INTO memories(
    id, workspace_id, user_global, kind, trust, lifecycle, current_revision_id,
    generated_fingerprint, pinned, created_at, updated_at
) SELECT id, workspace_id, user_global, kind, trust, lifecycle, current_revision_id,
    '', pinned, created_at, updated_at
FROM memories_v3;

INSERT INTO memory_revisions(
    id, memory_id, revision_number, kind, title, content, tags_json,
    resolution_state, trust, lifecycle, provenance_actor, provenance_source,
    provenance_labels_json, source_locator, created_at
) SELECT id, memory_id, revision_number, kind, title, content, tags_json,
    resolution_state, trust, lifecycle, provenance_actor, provenance_source,
    provenance_labels_json, source_locator, created_at
FROM memory_revisions_v3;

CREATE TABLE memory_aliases (
    alias_memory_id TEXT PRIMARY KEY,
    canonical_memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL,
    CHECK (alias_memory_id <> canonical_memory_id)
) STRICT;

CREATE TABLE usage_daily (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    day TEXT NOT NULL,
    hits INTEGER NOT NULL DEFAULT 0 CHECK (hits >= 0),
    opportunities INTEGER NOT NULL DEFAULT 0 CHECK (opportunities >= 0 AND hits <= opportunities),
    PRIMARY KEY (memory_id, day)
) WITHOUT ROWID, STRICT;

CREATE TABLE usage_lifetime (
    memory_id TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    hits INTEGER NOT NULL DEFAULT 0 CHECK (hits >= 0),
    opportunities INTEGER NOT NULL DEFAULT 0 CHECK (opportunities >= 0 AND hits <= opportunities)
) STRICT;

CREATE TABLE usage_session_hits (
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    consumer_digest BLOB NOT NULL,
    first_delivered_at TEXT NOT NULL,
    PRIMARY KEY (memory_id, consumer_digest)
) WITHOUT ROWID, STRICT;

CREATE TABLE skill_promotions (
    id TEXT PRIMARY KEY,
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    revision_id TEXT NOT NULL REFERENCES memory_revisions(id) ON DELETE CASCADE,
    actor TEXT NOT NULL,
    output_path TEXT NOT NULL,
    output_fingerprint TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

INSERT INTO memory_aliases(alias_memory_id, canonical_memory_id, created_at)
SELECT alias_memory_id, canonical_memory_id, created_at FROM memory_aliases_v3;
INSERT INTO usage_daily(memory_id, day, hits, opportunities)
SELECT memory_id, day, hits, opportunities FROM usage_daily_v3;
INSERT INTO usage_lifetime(memory_id, hits, opportunities)
SELECT memory_id, hits, opportunities FROM usage_lifetime_v3;
INSERT INTO usage_session_hits(memory_id, consumer_digest, first_delivered_at)
SELECT memory_id, consumer_digest, first_delivered_at FROM usage_session_hits_v3;
INSERT INTO skill_promotions(id, memory_id, revision_id, actor, output_path, output_fingerprint, created_at)
SELECT id, memory_id, revision_id, actor, output_path, output_fingerprint, created_at FROM skill_promotions_v3;

DROP TABLE memory_aliases_v3;
DROP TABLE usage_daily_v3;
DROP TABLE usage_lifetime_v3;
DROP TABLE usage_session_hits_v3;
DROP TABLE skill_promotions_v3;
DROP TRIGGER IF EXISTS memories_current_revision_insert;
DROP TRIGGER IF EXISTS memories_current_revision_update;
DROP TABLE memory_revisions_v3;
DROP TABLE memories_v3;

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

CREATE UNIQUE INDEX memories_generated_fingerprint_unique
    ON memories(workspace_id, generated_fingerprint)
    WHERE trust = 'generated' AND lifecycle = 'active';

CREATE TABLE curation_jobs (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL CHECK (reason IN ('periodic', 'pre_compact', 'session_end')),
    priority INTEGER NOT NULL CHECK (priority >= 1 AND priority <= 3),
    session_digest BLOB NOT NULL,
    source_watermark INTEGER NOT NULL CHECK (source_watermark >= 0),
    thread_locator_ciphertext BLOB NOT NULL DEFAULT X'',
    snapshot_ciphertext BLOB NOT NULL DEFAULT X'',
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'running', 'retry_wait', 'complete', 'terminal')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TEXT,
    expires_at TEXT NOT NULL,
    provider_name TEXT NOT NULL DEFAULT '',
    safe_error_class TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(session_digest, source_watermark),
    CHECK (length(session_digest) > 0 AND length(session_digest) <= 64),
    CHECK (length(thread_locator_ciphertext) <= 131072),
    CHECK (length(snapshot_ciphertext) <= 262144),
    CHECK (length(provider_name) <= 128 AND instr(provider_name, char(0)) = 0 AND instr(provider_name, char(10)) = 0 AND instr(provider_name, char(13)) = 0),
    CHECK (length(safe_error_class) <= 64 AND instr(safe_error_class, char(0)) = 0 AND instr(safe_error_class, char(10)) = 0 AND instr(safe_error_class, char(13)) = 0)
) STRICT;

CREATE INDEX curation_jobs_claim_idx ON curation_jobs(state, next_attempt_at, priority, created_at);
CREATE INDEX curation_jobs_expiry_idx ON curation_jobs(expires_at);

CREATE TABLE curation_session_counters (
    session_digest BLOB PRIMARY KEY,
    prompt_count INTEGER NOT NULL DEFAULT 0 CHECK (prompt_count >= 0),
    last_watermark INTEGER NOT NULL DEFAULT 0 CHECK (last_watermark >= 0),
    expires_at TEXT NOT NULL,
    CHECK (length(session_digest) > 0 AND length(session_digest) <= 64)
) STRICT;

CREATE INDEX curation_session_counters_expiry_idx ON curation_session_counters(expires_at);

DELETE FROM memory_fts;
INSERT INTO memory_fts(title, content, tags, memory_id)
SELECT revisions.title, revisions.content, revisions.tags_json, memories.id
FROM memories
JOIN memory_revisions AS revisions ON revisions.id = memories.current_revision_id
WHERE memories.trust IN ('verified', 'generated')
  AND memories.lifecycle = 'active'
  AND revisions.trust IN ('verified', 'generated')
  AND revisions.lifecycle = 'active'
ORDER BY memories.id;
