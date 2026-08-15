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

CREATE TABLE idempotency_requests (
    caller TEXT NOT NULL,
    operation TEXT NOT NULL,
    key_digest BLOB NOT NULL,
    request_digest BLOB NOT NULL,
    target_ids_json TEXT NOT NULL DEFAULT '[]',
    safe_result_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    PRIMARY KEY (caller, operation, key_digest)
) WITHOUT ROWID, STRICT;

CREATE INDEX idempotency_requests_expiry
    ON idempotency_requests(expires_at);

CREATE TABLE skill_promotions (
    id TEXT PRIMARY KEY,
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    revision_id TEXT NOT NULL REFERENCES memory_revisions(id) ON DELETE CASCADE,
    actor TEXT NOT NULL,
    output_path TEXT NOT NULL,
    output_fingerprint TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE deletion_receipts (
    id TEXT PRIMARY KEY,
    operation_id TEXT NOT NULL UNIQUE,
    memory_id TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    safe_metadata_json TEXT NOT NULL DEFAULT '{}'
) STRICT;
