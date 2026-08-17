# Talaria-Mem v0.0.1 Design

Status: approved target design; current implementation status is tracked in
`docs/IMPLEMENTATION_STATUS.md`.

Date: 2026-08-15

Milestone: pre-v0.1.0 exploration

This document is the normative target contract. It does not assert that every
listed CLI, hook, platform installer, or acceptance receipt is wired in the
current untagged checkout.

## 1. Purpose

Talaria-Mem is a local-first, workspace-scoped memory service for Codex. The
first release proves one end-to-end journey:

1. Install one native binary.
2. Run `talaria-mem setup codex`.
3. Infer or explicitly bind the current repository to a workspace.
4. Explicitly create memory through CLI, MCP, or import.
5. Review unverified items and explicitly assert trust where appropriate.
6. Start or resume a Codex session.
7. Receive relevant, locally secret-scanned workspace and user memory.
8. Search, update, and forget through CLI or MCP, and inspect the read-only
   generated Markdown projection.

v0.0.1 has no transcript ingestion and no model extraction. The local daemon
makes zero outbound network connections.

## 2. Scope

### 2.1 Included in v0.0.1

- Local, single-user operation.
- Codex-first integration through a SessionStart hook and MCP.
- Explicit memory creation, update, retrieval, confirmation, pinning,
  forgetting, purging, import, and export.
- Workspace and user-global scopes.
- Git-derived workspace inference with an explicit first-use warning.
- Explicit workspace binding and transactional workspace merge.
- SQLite as the canonical store and FTS5 lexical retrieval.
- Revisions that are immutable while retained, tombstones, and transactionally
  scheduled deterministic Markdown projection.
- Betterleaks at every content boundary.
- Usage-aware ranking and conservative pruning recommendations.
- Authenticated loopback HTTP.
- macOS LaunchAgent and Linux systemd-user lifecycle setup.

### 2.2 Excluded from v0.0.1

- Cloud extraction or any other cloud model call.
- Local model extraction.
- Transcript ingestion through PreCompact or SessionEnd.
- Embeddings or vector retrieval.
- Bidirectional automatic Markdown synchronization.
- Multi-user or multi-tenant service operation.
- Database eviction, rotation, sharding, or capacity budgets.
- NPX distribution and Windows service integration.

Deferred work is recorded in the repository `ROADMAP.md`.

## 3. Trust boundary and invariants

The v0.0.1 trust boundary is one operating-system user on one machine. The
daemon accepts only authenticated loopback traffic. It does not accept remote
clients, hostile multi-user input, or network filesystem deployment as a
supported surface.

Codex shell execution under that same user is trusted in v0.0.1. Talaria cannot
distinguish a human-entered CLI command from a Codex-entered command without an
external authorization mechanism. The CLI `--verified` flag is therefore an
explicit trust assertion and workflow boundary, not proof of human presence.

The following are target release blockers rather than deferred hardening:

- A detected or uncertain secret must not be returned to Codex.
- Unverified or quarantined memory must not enter automatic SessionStart or
  MCP-delivered model context. Explicit CLI inspection belongs to the declared
  trusted same-user boundary.
- Authentication must not be bypassable through another local web origin.
- A failed mutation must not partially update revision, FTS, or projection
  intent.
- A destructive operation must not run without explicit user authorization.
- A migration failure must leave a verified recovery path and keep the daemon
  unready.
- The runtime must have no outbound network path.
- SessionStart, search, import, and individual memory operations must remain
  within explicit item, byte, and concurrency bounds.

## 4. Runtime architecture

One local daemon owns SQLite and every mutation. Hooks, MCP tools, and CLI
commands call the same application services; none writes the database
independently.

The sole exception is an explicit offline recovery process. Daemon and offline
maintenance contend on one global lifecycle/database lock stored outside the
database. The winner holds it through open/stop verification, sidecar handling,
replacement or cleanup, invariant checks, and service-start eligibility. A
concurrent daemon or second maintenance process fails without touching files.

```text
Codex SessionStart hook ─┐
CLI ─────────────────────┼─> authenticated control/application services
MCP Streamable HTTP ─────┘                 │
                                           ├─> Betterleaks boundary
                                           ├─> SQLite + FTS5
                                           └─> transactional outbox
                                                       │
                                                       └─> Markdown projector
```

### 4.1 Interfaces

- `/mcp` uses the official MCP Go SDK and Streamable HTTP.
- `/control/v1/session-start` is described by OpenAPI and implemented with
  oapi-codegen.
- `/healthz` reports process liveness only.
- `/readyz` reports storage, scanner, migration, and projection readiness.
- Vacuum bundles and lints the OpenAPI sources before code generation.

All API errors use a versioned envelope containing a stable code, safe message,
receipt ID, and retryability flag. Error responses never echo submitted content
or secret matches.

Wire identifiers are UUIDv7 strings. Timestamps are UTC RFC 3339 with
nanoseconds. Enumerations reject unknown values. JSON objects reject duplicate
keys. Unless a narrower route limit is stated, authenticated HTTP request bodies
are limited to 1 MiB.

### 4.2 SessionStart

v0.0.1 installs only SessionStart. PreCompact and SessionEnd are not installed.
The versioned SessionStart request accepts only event ID, session ID, hook name,
and working directory. The decoder discards unknown fields before application
dispatch. If Codex includes transcript-related fields, Talaria does not open,
copy, log, or persist them.

SessionStart resolves the working directory through the persisted workspace
binding. It never accepts an event-supplied workspace override and never
retrieves another workspace implicitly.

Only active, verified, non-quarantined memories are eligible. Selection has no
lexical query and therefore uses this deterministic priority order:

1. Pinned standing instructions.
2. Other pinned memories.
3. Unresolved failures.
4. Remaining memories by `startup_score = combined_boost`, the capped
   freshness-plus-usage value defined in section 9 without lexical relevance.

Eligible workspace/global items are first deduplicated by exact
`normalization-v1` content, with the workspace item winning, and then assigned
to tiers. Within a tier, workspace scope precedes user-global scope, then score
descends, current-revision timestamp descends, and memory ID ascends.

The serialized response contains at most 20 whole memories and 32 KiB,
including framing. Individual memory content is limited to 8 KiB. Content is
never partially truncated; the response includes included and omitted counts
and a safe truncation receipt ID. It also returns the resolved workspace ID for
subsequent MCP calls.

Pinned standing instructions reserve at most five items and 8 KiB in each of
workspace and global scope. Pinning is rejected if it would overflow that
scope's reserve. An inconsistent legacy state keeps readiness false instead of
silently omitting a pinned instruction.

Every selected item is scanned again before serialization. A newly detected
finding quarantines that item and excludes it. Scanner failure or uncertainty
returns no memory content. The response clearly delimits every memory as
untrusted reference data and includes ID, scope, kind, revision, and provenance;
memory text is never presented as system instructions.

### 4.3 Lifecycle

- `talaria-mem setup codex --dry-run` reports exact configuration and service
  changes. `--apply` installs them.
- macOS uses a LaunchAgent.
- Linux uses a systemd user service.
- `talaria-mem daemon` supports foreground and diagnostic operation.
- A missing daemon yields an actionable hook error.
- Hooks never download, upgrade, or replace a binary.

Setup parses and preserves unknown Codex configuration, makes a permissioned
same-directory backup, writes through a same-directory temporary file, fsyncs,
renames, and fsyncs the parent directory. Repeated setup is idempotent. Port or
service-name collisions fail before mutation. Partial service/configuration
failure rolls back from the verified backup. `setup codex --remove --dry-run`
and `--apply` remove only entries matching the recorded installation
fingerprint.

SQLite uses one writer and concurrent WAL readers. Background projection is
bounded and idempotent.

## 5. Security and secret handling

### 5.1 Loopback authentication

- Bind only literal `127.0.0.1` and `::1` addresses.
- Generate one random per-install bearer token authorizing every loopback
  route. Capability-separated tokens are intentionally deferred.
- Compare tokens in constant time.
- Accept credentials only in the `Authorization: Bearer` header; reject query
  and cookie credentials.
- Accept only the configured literal `127.0.0.1:port` and `[::1]:port` Host
  authorities.
- Reject forwarding headers, `Origin: null`, and every nonempty Origin. A
  missing Origin is accepted for the authenticated native clients.
- Do not enable CORS, do not expose GET mutations, and require JSON content
  types for mutations.
- Apply bounded request bodies, concurrency, and timeouts.
- Require mode `0600` for token and configuration files.
- Provide `talaria-mem token rotate`.

The v0.0.1 process contains no generic outbound HTTP client or automatic update
check. Hosted operation is not a hidden switch in the local binary.

### 5.2 Betterleaks

Betterleaks is embedded behind a Talaria-owned scanner port. Talaria pins both
the module version and a reviewed rule configuration.

- Provider validation and every scanner network feature are permanently
  disabled.
- Rule upgrades are explicit and gated by comparative fixtures and a full
  active-data rescan.
- Creation, update, import, and confirmation scan every persisted textual
  field, including title, content, tags, and provenance labels. One finding
  rejects the whole mutation or import batch; the previous revision remains
  active.
- SessionStart, MCP and CLI reads, export, Markdown projection, and skill
  promotion scan before producing content.
- A finding discovered in existing data moves the memory to `quarantined` and
  exposes safe metadata only until CLI update or purge resolves it.
- Logs, errors, receipts, and diagnostics contain IDs, rule IDs, and safe
  offsets only, never submitted content or match fragments.
- Rejections report only rule/category and safe location metadata.
- Scanner error, timeout, panic, cancellation, or uncertain result fails
  closed.
- No reversible redaction map is retained.

Readiness remains false during a rule-upgrade rescan and projection rebuild.
Every content route has a fixture covering success, finding, scanner failure,
and absence of canary text from logs and error paths.

Talaria-Mem is not a password vault. Users must store references to secret
locations, never the secret values.

### 5.3 Managed filesystem

Talaria-owned directories require the current user's ownership and mode `0700`.
The database, WAL, SHM, cryptographic root key, migration backups, projections,
temporary files, token, and configuration require mode `0600`. Managed roots
and targets must not be symlinks. Temporary files are created with no-follow
semantics in the destination directory.

`skill promote --output` and setup refuse symlink targets and do not overwrite
an existing file by default. An allowed overwrite requires both `--force` and
the expected current fingerprint. File replacement fsyncs the file and parent
directory. Startup and `doctor` report ownership, mode, sidecar, and unsafe-path
violations without printing content.

## 6. Workspace identity

Workspace resolution uses this order:

1. An explicit repository binding.
2. A normalized, credential-free Git remote identity: `host/owner/repository`.
3. A repository-root fingerprint when no usable remote exists.
4. The absolute path as the final fallback.

The first inferred binding is persisted and emits a warning similar to:

```text
Workspace inferred as araihu/talaria-mem. Use --workspace <name> or
`workspace bind` to select an existing workspace.
```

Talaria never silently selects a similarly named existing workspace.

### 6.1 Workspace merge

```text
talaria-mem workspace merge <source> <target> --dry-run
talaria-mem workspace merge <source> <target> --apply <receipt>
```

Merge runs transactionally. It:

- Deduplicates only exact normalization-v1 content: valid UTF-8, Unicode NFC,
  CRLF/CR converted to LF, with every other byte preserved. The comparison
  covers kind, title, body, and sorted canonical tags.
- Preserves every provenance edge and revision chain.
- Retains conflicting non-identical items and flags them for review.
- Rebinds source repositories to the target.
- Converts the source into a permanent redirect.
- Is idempotent when repeated.

For an exact duplicate, the target memory ID remains canonical and the source
ID becomes a permanent alias. A nonduplicate source ID is preserved. If source
and target already use the same ID for different content, v0.0.1 rejects the
merge; the dry-run instructs the user to export/reimport the source item with a
new ID before retrying. Redirects are flattened to the final target, cycles are
rejected, and redirect/alias lookup is consistent through CLI and MCP. v0.0.1
does not support workspace deletion. Merge schedules projections for both
source and target scopes in its database transaction.

The dry-run receipt binds source, target, both workspace revision watermarks,
planned ID/alias changes, and expiry. Apply rejects a changed watermark,
expired receipt, target reversal, or receipt reuse.

No model decides whether two memories are semantically equivalent.

## 7. Memory model

### 7.1 Kinds

- State or fact.
- Procedure.
- Failure or lesson.
- Standing instruction.

Failure memories carry `resolution_state = open | resolved`, defaulting to
`open`. Other kinds reject the field. Create/update may transition it with the
same expected-revision and trust rules as content; no hidden state mutation is
allowed.

Standing instructions may be created only through CLI add/update with the
explicit `--verified` assertion. MCP and import reject that kind. A procedure
remains searchable memory until an explicit command promotes it to a
`SKILL.md`:

```text
talaria-mem skill promote <memory-id> --expected-revision <revision> \
  --output <path> --dry-run
talaria-mem skill promote <memory-id> --apply <receipt>
```

Promotion accepts only an active, verified, non-quarantined procedure. The
dry-run receipt binds source revision, content fingerprint, output path,
existing-output fingerprint, and expiry. Apply rejects any change. Promotion
records actor, source revision, timestamp, and output fingerprint.

### 7.2 Trust and lifecycle

Memory trust is independent from lifecycle:

- `verified`: a CLI create/update explicitly carrying `--verified`, or an exact
  revision confirmed through the CLI.
- `unverified`: every create/update by default, every MCP/import write, and
  future extractor output.

MCP has no verified field and confirmation is absent from MCP. MCP search/get
and SessionStart expose verified memory only. CLI can inspect safe unverified
metadata/content. `memory confirm <id> --expected-revision <revision>` compares
the exact reviewed revision, rescans it, and appends a content-identical
verified revision in one transaction; a concurrent change rejects confirmation.

The trust transition matrix is normative:

- CLI create/update without `--verified`: unverified.
- CLI create/update with `--verified`: verified after scanning.
- CLI confirmation of an exact revision: verified.
- MCP create/update: unverified, with no override.
- Import: unverified, with no override.
- Restore after forget: unverified unless the CLI supplies `--verified`.

Confirmation cannot convert MCP/import provenance into a standing instruction;
that requires a CLI update declaring the kind together with `--verified`.

Lifecycle states are:

- `active`: eligible for trust- and quarantine-filtered retrieval/projection.
- `quarantined`: retained but excluded from content outputs after a scanner
  finding.
- `forgotten`: tombstoned and excluded, with revisions retained.
- `purged`: content and revisions removed from every Talaria-managed live
  representation, followed by secure-delete maintenance.

`forget` is reversible. Purge uses a crash-resumable operation:

1. `memory purge --dry-run` creates a short-lived, single-use receipt bound to
   workspace, memory ID, expected revision, affected projection and backup IDs,
   and expiry. It contains no memory content.
2. Applying that receipt quiesces new requests and drains readers.
3. One transaction inserts a non-content `purge_pending` record and removes
   active identity, revisions, FTS entries, linkable usage telemetry, pending
   outbox material, target-linked idempotency rows, and skill-promotion links.
4. The daemon checkpoints and truncates WAL, rebuilds and durably replaces
   projections, deletes every matching backup from the managed inventory, and
   fsyncs affected directories.
5. It marks the operation complete and issues the deletion receipt last.

The expected revision and preview inventory must still match at apply time.
Inventory revision watermarks identify backups created while the memory
existed; every such managed backup is matching even when deleting it also
removes recovery history for unrelated memories.
Receipts expire, are single-use, and retries are idempotent. Startup resumes
every incomplete `purge_pending` operation before readiness. A purge is never
reported complete while a managed cleanup step fails.

Talaria enables SQLite core `secure_delete=ON` and FTS5 `secure-delete=1` so
ordinary table pages and full-text index entries are scrubbed during deletion.
This prevents recovery through Talaria or ordinary access to its current
database files. It cannot promise forensic erasure from SSD wear-leveling,
copy-on-write filesystem history, operating-system snapshots, or backups that
Talaria does not control. Full-disk encryption and external snapshot policy
remain the user's media-level controls.

## 8. Canonical storage

SQLite is canonical. Markdown is a rebuildable projection.

Core logical tables are:

- `workspaces`, `workspace_bindings`, `workspace_redirects`, and
  `memory_aliases`.
- `memories` with stable identity, scope, kind, trust, lifecycle, and current
  revision pointer.
- `memory_revisions` with immutable content and provenance.
- `memory_fts` containing active, verified, non-quarantined current revisions
  only.
- `usage_daily` for the rolling 90-day retrieval window.
- `usage_lifetime` for diagnostic aggregate counters.
- `outbox` containing pending projection scope and revision watermark.
- `projection_state`, `skill_promotions`, and `deletion_receipts`.
- `purge_operations` for non-content resumable cleanup state.
- `managed_backups`, a rebuildable database mirror of authenticated sidecar
  manifests stored outside the database rollback unit. Each manifest contains
  backup ID, path, hash, permissions, creation cause, schema version, database
  revision watermark, lifecycle, and ownership.
- `idempotency_requests` with caller, operation, key, domain-separated keyed
  request digest, target IDs, and safe result metadata.

Session identifiers used for access deduplication are HMAC-derived with a local
key. Raw Codex/MCP session identifiers and query text are not stored or logged.
Codex uses the hook session; MCP uses its transport session; each CLI invocation
is one explicit consumer session. A memory counts at most once per consumer
session.

One protected root key lives outside the database rollback unit. Versioned
HKDF-SHA256 labels derive separate subkeys for session deduplication,
idempotency digests, and backup-manifest authentication. The bearer token is
independent, and `token rotate` changes only that bearer token. The root and all
derived subkeys remain fixed throughout v0.0.1; manifests still record their key
version for future migration. Root-key loss or corruption blocks readiness and
restore rather than silently generating a replacement. Root rotation and
crash-safe manifest reauthentication are deferred.

Usage is bucketed by UTC day. Startup and a daily bounded task delete detailed
rows older than 90 days, including catch-up after downtime. Lifetime aggregates
contain counts only. Purge removes every row linkable to the memory.

### 8.1 Mutation transaction

Every memory mutation performs the following in one SQLite transaction:

1. Insert an immutable revision or tombstone.
2. Move the memory's current revision pointer.
3. Update the current-revision FTS index.
4. Append a minimal projection event.

Trust follows the actor/operation matrix in section 7.2. A transition to
unverified, quarantined, forgotten, or purged removes its FTS row and schedules
projection cleanup in the same transaction. A newly discovered scanner finding
atomically sets quarantine, removes FTS, and appends outbox intent; projection
readiness remains false until the safe durable replacement completes. A failed
scan or optimistic-revision conflict performs none of the four mutation steps.

`memory restore <id> --expected-revision <tombstone> [--verified]` performs a
fresh scan, appends a new active revision from the last retained content, and
updates FTS/outbox atomically. It rejects stale tombstones and purge receipts.

The outbox contains scope and revision watermark, not duplicated memory bodies.
Delivered outbox entries are not retained long-term.

### 8.2 Markdown projection

One projector holds the scope lock, groups events by scope, and renders the
complete active, verified, non-quarantined document from SQLite in a versioned
UTF-8 format with stable memory-ID ordering and LF line endings. It creates a
mode-`0600` temporary file in the destination directory, fsyncs it, renames it
without following symlinks, fsyncs the parent directory, and verifies the
generated fingerprint. Only then does it delete delivered outbox entries.

A crash after rename but before deletion safely replays the same deterministic
render. Startup removes only stale temporary files matching Talaria's recorded
name and ownership. Failed events retain attempt count, next-attempt time, and
a sanitized error.

Generated documents carry a machine-owned header and fingerprint. Unexpected
external edits place that scope in `drifted` state and are never overwritten or
merged automatically. Canonical SQLite reads remain available while readiness
and `doctor` report the drift.
`projection rebuild --scope <id> --force --expected-fingerprint <hash>`
explicitly replaces the file; a changed fingerprint rejects the command.

```text
talaria-mem import <file> --workspace <name> --dry-run
talaria-mem import <file> --workspace <name> --apply
```

Import validates and commits through SQLite before projection.

## 9. Retrieval and ranking

v0.0.1 uses FTS5 lexical retrieval only. `memory_fts` is a normal-content FTS5
table with `title`, `content`, `tags`, and an unindexed `memory_id`, using
`tokenize='unicode61 remove_diacritics 2'` and FTS5 `secure-delete=1`. It
contains exactly one row for each active, verified, non-quarantined current
revision. Trigram indexing is deferred until real retrieval fixtures justify
its index cost.

The canonical rebuild deletes and repopulates FTS in one exclusive transaction
from active, verified, non-quarantined current revisions. `doctor` compares row IDs,
trust/lifecycle eligibility, and normalized content hashes;
`doctor --repair=fts` performs the rebuild only after an explicit dry-run.
Update, forget, purge, rollback, migration, Unicode, and
injected-statement-failure fixtures cover the invariant.

Only memories passing scope, trust, lifecycle, and FTS MATCH enter ranking.
The raw relevance is
`r = max(0, -bm25(memory_fts, 5.0, 1.0, 2.0, 0.0))`; title, content, and tags
therefore have weights 5, 1, and 2. Normalized relevance is `r / (1 + r)` and
must be greater than zero. Freshness and usage cannot rescue a nonmatch.

```text
freshness = 0.10 * 2^(-age_seconds / 2592000)

usage = min(
  0.20,
  0.20 * ln(1 + distinct_session_hits_90d) / ln(21)
)

combined_boost = min(freshness + usage, 0.25)

final_score = normalized_lexical_relevance * (1 + combined_boost)
```

The usage boost saturates at 20 distinct-session hits in the rolling 90-day
window. Freshness uses immutable memory creation time, an injected UTC clock,
and clamps negative age to zero. Calculations use IEEE-754 float64 without
decimal pre-rounding.

An access is counted only when usable memory content is delivered through
search, get/show, context injection, or an equivalent direct read. Each memory
counts at most once per consumer session. Indexing, projection, maintenance,
metadata-only mutation responses, and internal candidate evaluation do not
count.

Every delivered hit records a paired opportunity. Search additionally records
missed opportunities for eligible pre-boost candidates not delivered;
SessionStart and direct get/show record one synthetic opportunity and one hit
for each delivered memory. Therefore every rolling bucket maintains
`0 <= hits <= opportunities` while all delivered hits still feed the usage
boost.

Search orders final score descending, raw relevance descending, current
revision timestamp descending, then memory ID ascending. The implementation
uses an injected clock and fixed numeric fixtures so the same database/query
produces the same ordering.

Every result can explain:

- Raw and normalized lexical relevance.
- Freshness boost.
- Usage boost.
- Combined cap.
- Final score.

## 10. Pruning recommendations

Retrieval score answers whether a memory is useful for a query. Pruning score
answers whether evidence supports retaining it. Inactivity never subtracts
from lexical relevance.

For search, an eligible opportunity occurs when a memory passes the same scope,
trust, and lifecycle filters as the requesting interface, has positive raw
relevance, and appears in the top 20 pre-boost candidates ordered by raw
relevance and stable tie-breakers. SessionStart and direct get/show use the
paired synthetic opportunity defined in section 9.

Ratio-based pruning is ignored until both conditions hold:

- Memory age is at least 30 days.
- At least 20 eligible opportunities exist.

Pruning evaluates hits and opportunities in the latest 90 days. Lifetime
counters are diagnostic only.

For `n = opportunities` and observed hit rate `p = hits / n`, Talaria
calculates the one-sided optimistic 95% Wilson upper bound:

```text
z = 1.64485362695

upper = (
  p + z*z/(2*n)
  + z*sqrt(p*(1-p)/n + z*z/(4*n*n))
) / (1 + z*z/n)
```

A memory becomes a recommendation candidate only when that upper bound is
below the default absolute threshold of 5%. The threshold is
workspace-configurable. Talaria does not force a percentile quota.

Protected by default:

- Pinned memories.
- User-authored memories.
- Standing instructions.
- Failures whose modeled resolution state is `open`.
- Memories verified within the latest 30 days.

`--include-verified` overrides the user-authored/verified and recent-verification
protections only. It never overrides pinning, standing instructions, or open
failures in v0.0.1; those require an explicit individual forget or purge.

Commands:

```text
talaria-mem memory prune --workspace <name> --dry-run
talaria-mem memory prune --workspace <name> --apply
talaria-mem memory prune --workspace <name> --include-verified
talaria-mem memory explain <id>
```

`--apply` uses `forget`. Physical reclamation requires an explicit purge.
Absence of enough opportunities is not evidence of low impact.

## 11. SQLite operations and migrations

Talaria uses `modernc.org/sqlite`, avoiding CGO and preserving FTS5 support.
The database is created with incremental-vacuum capability and core
`secure_delete=ON`; the FTS5 table is configured with `secure-delete=1`.
v0.0.1 does not run automatic vacuuming or enforce an artificial size budget.

`talaria-mem doctor` reports database, freelist, and WAL sizes. `SQLITE_FULL`
is returned as an ordinary safe write failure rather than crashing or retrying
indefinitely. Reads and allocation-light diagnostics are attempted but are not
promised when the underlying filesystem itself is exhausted.

Before applying pending migrations, one process holds an exclusive database
and migration lock while the daemon remains offline/unready. It:

1. Reserves a deterministic backup ID and writes/fsyncs an HMAC-authenticated
   `pending` sidecar manifest before creating a backup file.
2. Runs `VACUUM INTO` with full synchronization into the ID-bound private
   mode-`0600` partial path.
3. Hashes and fsyncs the backup, atomically renames it to the manifest path,
   marks the sidecar `complete`, and fsyncs the backup directory.
4. Rebuilds the database inventory mirror from sidecars, opens the backup
   read-only, and runs integrity, foreign-key, schema, FTS, and application
   checks.
5. Applies embedded migrations to the live database with the modernc
   golang-migrate driver, one implicit transaction per migration.
6. Rejects ordinary migrations containing non-transactional maintenance such
   as `VACUUM`.
7. Records target, current, failed, and completed versions in a forward-only
   migration run journal.
8. Runs post-migration integrity, foreign-key, schema, FTS, and application
   checks.
9. Starts only after every check succeeds.

Startup reconciles every manifest, partial file, and complete file in the
owned backup directory before readiness. Missing, orphaned, unauthenticated, or
unexpected entries remain managed cleanup candidates and keep readiness false;
they are never silently ignored or deleted.

For an authenticated pending manifest, startup resumes finalization when the
partial database passes integrity checks; otherwise it records a cleanup
candidate. A pending manifest with no file and no started migration is safely
retired. Unknown or unauthenticated entries require
`db backup reconcile --dry-run`, which produces an expiring receipt bound to
path, ownership, fingerprint, and proposed quarantine/deletion. `--apply
<receipt>` runs under the global maintenance lock. Successful resume or cleanup
rebuilds the inventory mirror and restores readiness. Crash fixtures cover
every reconciliation action.

Each migration is atomic, but the pending batch is not: if migration 4 fails
after 1–3 commit, the schema remains at version 3. The exact partial version and
failure are recorded, the verified backup is retained, and readiness remains
false. Startup resumes forward from the recorded version when the same target
set is available.

`talaria-mem db restore --backup <id> --dry-run` creates an expiring receipt;
`--apply <receipt>` acquires the global maintenance lock, verifies the daemon is
stopped plus inventory identity, hash, permissions, and integrity, handles
database sidecars as one recovery unit, installs through same-directory
fsync/rename/fsync, and reopens the result for all invariant checks before
service-start eligibility. Because authenticated backup manifests live outside
the database rollback unit, restore rebuilds inventory without forgetting the
restored-from or newer backups. Failure injection covers every point before and
after manifest creation, backup creation, fsync, inventory rebuild, migration,
restore, and concurrent daemon startup. The live database is never renamed away
from an associated hot journal or WAL.

## 12. MCP and CLI contracts

Initial MCP tools:

```text
memory_search      memory_get
memory_create      memory_update
memory_pin         memory_forget
memory_explain
```

`memory_search` requires a UTF-8 query of at most 1 KiB and the workspace ID
returned by SessionStart; it searches that workspace followed by global scope
and returns 10 verified memories by default, at most 20 items and 64 KiB
serialized. CLI search uses the same item/byte bounds and ordering. Whole items
that would cross either bound are omitted; results include included/omitted
counts and a safe truncation receipt. Workspace-scoped MCP tools have no
cross-session default and reject a missing or redirected-unresolved workspace
ID. `memory_get` returns one verified memory by ID after the same scope checks.
Neither tool accepts an `include_unverified` switch.

Search normalizes the query to Unicode NFC and constructs a parameter-bound
FTS expression from literal terms. v0.0.1 does not accept caller-supplied raw
FTS operators or SQL fragments.

Create/update accept a title of at most 256 bytes, content of at most 8 KiB, at
most 20 tags of 64 bytes each, at most ten provenance labels of 128 bytes plus a
512-byte source locator, a declared kind, and an explicit or bound scope. Their
responses contain safe metadata rather than echoing unverified content. Updates,
pin, and forget require the expected revision and fail on conflict. Failure
create/update also accepts the versioned `resolution_state` enum. Pin accepts
verified, active, non-quarantined memory only.

Every mutation requires a caller-supplied idempotency key. The daemon retains
the key, operation, HMAC-domain-separated request digest, target IDs, and safe
result for 24 hours. Repeating the same key/digest returns the first result; the
same key with a different request digest returns conflict. Purge removes rows
linked to its target. Cleanup after downtime is bounded and deterministic.
The CLI generates a random key unless the user supplies one explicitly.

Initial CLI surface:

```text
setup codex
daemon
status
doctor

workspace list|show|bind|merge
memory add|list|review|search|get|update|confirm|pin|forget|restore|purge|prune|explain
import
export
skill promote
projection rebuild
db restore
db backup reconcile
token rotate
```

`memory review --trust=unverified` is the bounded discovery queue for default
CLI, MCP, and import writes. It orders creation timestamp then memory ID, returns
10 items by default, at most 20 whole items and 32 KiB, and includes ID,
revision, scope, provenance, and freshly scanned content. An opaque keyset cursor
continues after the last `(created_at, memory_id)` tuple; output includes omitted
count and next cursor. Creation/import result metadata always includes created
IDs and revisions, but the durable review queue does not depend on retaining
that command output. Confirmation uses the exact revision shown by review.

CLI creation/update is unverified unless the operator supplies `--verified`;
the flag has the trust-boundary meaning defined in section 3. Import is
all-or-nothing, always produces unverified memory, defaults to dry-run, supports
the versioned generated Markdown format and `talaria.memory.v1` JSON Lines, and
defaults to conflict failure rather than overwrite. Import accepts at most 10
MiB or 1,000 items per invocation. Export writes versioned JSON Lines or the
inspection Markdown format through the same output scanner and safe-file
protocol.

Stable CLI exits are: 0 success, 1 unexpected internal failure, 2
usage/validation, 3 revision or idempotency conflict, 4 authentication, 5
secret/quarantine refusal, 6 unavailable or incomplete maintenance, and 7 not
found. Human output goes to stderr when machine JSON is selected. No command
logs query or memory text.

At most eight read requests and one writer run concurrently. FTS queries have a
two-second execution deadline; mutations have a five-second deadline excluding
explicit maintenance operations. Exceeding a bound returns a safe typed error,
never partial content.

## 13. Implementation structure

The project is one Go module and one binary:

```text
cmd/talaria-mem/
internal/domain/
internal/application/
internal/adapters/sqlite/
internal/adapters/http/
internal/adapters/mcp/
internal/adapters/codex/
internal/projection/
internal/scanner/
internal/workspace/
api/openapi/
db/migrations/
docs/superpowers/specs/
ROADMAP.md
```

Primary dependencies:

- Cobra.
- modernc.org/sqlite.
- sqlc.
- golang-migrate's modernc SQLite driver.
- Betterleaks behind a repository-owned scanner interface.
- Official MCP Go SDK.
- oapi-codegen.
- Vacuum.

Hexagonal boundaries protect replacement points such as storage, scanner, MCP,
Codex hook transport, and projection. The implementation should not introduce
interfaces for functions that have no meaningful replacement boundary.

## 14. v0.0.1 acceptance gates

- Explicit memory survives restart and appears in FTS retrieval.
- SessionStart fixtures prove verified-only admission, workspace/global
  precedence, exact deduplication, stable ordering, pin reserves, 20-item and
  32-KiB bounds, whole-item omission, and safe scanner failure.
- A transcript-path canary plus filesystem-open evidence proves SessionStart
  never reads, copies, persists, or logs the transcript.
- Actor/operation fixtures prove default-unverified behavior, explicit CLI
  `--verified`, exact-revision confirmation race rejection, MCP's lack of a
  verified override, and standing-instruction admission rules.
- Review-queue fixtures prove bounded/paginated rediscovery of every unverified
  ID/revision after lost CLI/import output and restart.
- Imported/MCP imperative-text fixtures remain unverified and absent from MCP
  retrieval and SessionStart within the declared trusted-same-user boundary.
- Skill-promotion fixtures require active/verified/non-quarantined state and a
  revision/fingerprint-bound receipt.
- First inferred workspace emits the required warning.
- Explicit binding and the normalization/ID/alias/redirect merge matrix work
  transactionally and reject cycles.
- Revision, FTS update, and outbox insertion are atomic.
- Forget/restore fixtures prove tombstone revision binding, fresh scanning,
  trust outcome, conflict rejection, restart behavior, and atomic FTS/outbox.
- FTS row/hash checks enforce the single active/verified/non-quarantined
  predicate through update, quarantine, forget, restore, purge, rollback, and
  migration repair.
- Projection failpoints after create, file fsync, rename, directory fsync,
  verification, and acknowledgement prove deterministic replay and drift
  quarantine.
- Quarantine fixtures prove atomic FTS removal/outbox intent and readiness
  blocking until the old projection is durably scrubbed.
- Secret fixtures cover create, update, import, confirm, SessionStart, MCP/CLI
  reads, export, projection, skill promotion, logs, errors, and rule upgrades.
- Every daemon operation passes under denied egress while process-specific
  socket/DNS evidence proves the daemon performs no `connect` call. Separate
  CLI/hook/MCP-client-mode evidence permits only the configured literal
  loopback destination. A dependency/static audit finds no reachable daemon
  dialing path, and scanner network settings cannot be enabled by configuration.
- Host/Origin/forwarding-header, query/cookie credential, stale-token,
  cross-origin, JSON-method, and every-route authentication fixtures pass.
- Managed-path fixtures enforce owner/mode/no-follow/no-clobber behavior.
- Root-key loss/corruption, purpose-separated derivation, bearer-token-only
  rotation, idempotency replay, manifest restore, and key-version fixtures pass.
- CLI/MCP search fixtures prove shared ordering, 20-item/64-KiB caps, whole-item
  omission, metadata limits, and safe truncation receipts.
- Usage/opportunity accounting and score explanations match fixed fixtures.
- Pruning fixtures freeze clock, BM25 normalization, stable ordering, the
  one-sided 95% Wilson bound, retention catch-up, override precedence, and
  threshold.
- Purge crash injection after every boundary proves startup resumption and
  removal from live tables, retained revisions, FTS5, usage, outbox,
  projections, sidecars, temporary files, and inventoried backups. Closed-file
  canaries test managed bytes without claiming media-level forensic erasure.
- Backup failpoints prove pre-created authenticated manifests, orphan/partial
  resume/receipt-bound cleanup, readiness recovery, inventory reconstruction
  after restore, and purge coverage.
- Migration failure fixtures prove exact partial version, forward resume,
  verified backup inventory, full restore, and daemon unready state.
- Global-lock fixtures prove daemon and offline maintenance cannot overlap
  through restore, backup reconciliation, or service-start eligibility.
- Setup fixtures prove dry-run, unknown-config preservation, idempotency,
  collision refusal, rollback, and fingerprint-bound removal.
- Vacuum lint/bundle and generated-code checks pass.
- macOS LaunchAgent and Linux systemd-user setup pass their declared platform
  tests.

## 15. Rejected or deferred alternatives

- Markdown-canonical storage: rejected in favor of transactional SQL followed
  by projection.
- `mattn/go-sqlite3`: rejected because it requires CGO.
- Gitleaks: rejected after comparison in favor of Betterleaks.
- MCP SSE: rejected as legacy; use Streamable HTTP.
- Cloud extraction: excluded.
- Automatic bidirectional Markdown merge: rejected.
- Automatic semantic workspace deduplication: rejected.
- Embeddings in v0.0.1: deferred.
- Automatic memory eviction for database size: deferred.
- Capability-separated loopback tokens: deferred; v0.0.1 uses one token within
  the declared single-user boundary.
- Database-file rename promotion during migration: rejected.

## 16. Maturity receipt

- Stage: pre-v0.1.0 exploration.
- Current journey: explicit local memory creation through safe SessionStart
  retrieval and Markdown projection.
- Active trust boundary: one operating-system user, authenticated loopback,
  local files, no outbound runtime network.
- Current blockers: secret exposure, auth bypass, partial mutation, unsafe
  destructive behavior, false recovery claims, and failure of the declared
  journey.
- Deferred candidate gates: broader compatibility, fuzzing, reproducible
  release, hostile local adversaries, additional platforms, and scale.
