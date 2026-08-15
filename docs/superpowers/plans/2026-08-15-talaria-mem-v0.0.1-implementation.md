# Talaria-Mem v0.0.1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `$run-reviewed-worktree-development` to implement this plan task-by-task through frozen, independently reviewed checkpoints. Every task uses TDD and ends with an independently testable commit.

**Goal:** Build the complete local, single-user Talaria-Mem v0.0.1 journey described by the approved architecture specification, with SQLite-canonical memory, Betterleaks boundary scanning, authenticated local interfaces, deterministic projection, recovery, and exhaustive acceptance evidence.

**Architecture:** One Go module and one binary expose one authenticated loopback daemon. CLI, MCP, and Codex SessionStart share application services; only the daemon mutates SQLite. SQLite and FTS5 are canonical, an authenticated transactional outbox schedules deterministic Markdown projection, and all trust/security decisions are enforced before content leaves the process.

**Tech Stack:** Go; modernc.org/sqlite with FTS5; sqlc; golang-migrate's modernc SQLite driver; Cobra; official MCP Go SDK with Streamable HTTP; Betterleaks behind a Talaria-owned scanner port; oapi-codegen; Vacuum; Go test, race, vet, and failpoint fixtures.

**Spec:** `docs/superpowers/specs/2026-08-15-talaria-mem-v0.0.1-design.md` (SHA-256 `1a7094653794117f126fab7bca0baf58efeee8985c3eaa73e44608b9992b56c5`)

## Global Constraints

- Implement only the included v0.0.1 scope; all post-v0.0.1 work in `ROADMAP.md` remains deferred.
- Use one Go module and one binary; preserve the implementation structure in specification section 13.
- The daemon makes zero outbound network connections and has no cloud or local model extraction path.
- SessionStart is the only Codex hook; do not add PreCompact, SessionEnd, transcript ingestion, or transcript persistence.
- Default every write to unverified; only CLI create/update with explicit `--verified` or CLI exact-revision confirmation can produce verified memory.
- MCP and import have no verified override; unverified or quarantined content never enters MCP or automatic SessionStart context.
- Betterleaks is embedded, pinned, offline-only, fail-closed, and applied at every declared content boundary.
- SQLite is canonical; Markdown is rebuildable and is written only through the transactional outbox/projector protocol.
- Use `normalization-v1`, UUIDv7 wire identifiers, UTC RFC3339 timestamps with nanoseconds, strict enums, stable error envelopes, and exact size/concurrency/time limits from the spec.
- Protect root key, bearer token, configuration, database, WAL, SHM, backups, projections, and temporary files with the declared ownership and modes; reject symlinks and unsafe replacement.
- Every mutating operation is optimistic-revision checked, idempotent, scanner-gated, and tested for atomic revision/FTS/outbox behavior.
- Use TDD: write one behavior test, run it and capture the expected RED failure, implement the minimum behavior, capture GREEN, then refactor only while green.
- Never edit generated code by hand. Vacuum and bundle OpenAPI sources before oapi-codegen output.
- Preserve raw command output, exit codes, file paths, commit/tree identities, and all Git status classes in every receipt.
- Acceptance does not authorize merge, push, PR, tag, release, publish, deploy, rollback, branch deletion, worktree removal, or control-plane archival.

## Dependency DAG and checkpoint map

```text
T1 bootstrap
  ├── T2 domain and ports
  │     ├── T3 SQLite schema/repository
  │     ├── T4 Betterleaks scanner
  │     └── T6 workspace identity
  ├── T5 application mutation/trust/idempotency (T2,T3,T4; KeyDeriver port from T2)
  ├── T7 projection/outbox worker (T3,T5)
  ├── T8 retrieval/usage/pruning (T3,T5; KeyDeriver port from T2)
  ├── T9 security/keys/auth/managed filesystem (T2,T3,T4)
  ├── T10 HTTP/OpenAPI/CLI/MCP surfaces (T5,T6,T8,T9)
  ├── T11 SessionStart/Codex hook (T5,T6,T8,T9,T10)
  ├── T12 review/import/export/skill promotion (T5,T6,T7,T8,T9,T10)
  ├── T13 purge/backups/migrations/recovery/maintenance lock (T3,T5,T7,T9)
  ├── T14 setup/status/doctor/platform lifecycle (T9,T10,T13)
  └── T15 integrated acceptance and final receipt (T1–T14)
```

T2 owns the `KeyDeriver` port and its domain-separated purpose contract. T5 and
T8 depend only on that port and deterministic test doubles; they never import
root-key storage or persist raw session identifiers. T9 implements and wires the
real root-key/HKDF provider before CP3 runtime composition. T10 consumes the
T8 retrieval service explicitly; no surface task may stub or duplicate search,
usage, pruning, or explain behavior.

Major checkpoints:

- **CP0 — Plan:** this plan, the complete DAG, ownership, and gate matrix are frozen and independently accepted.
- **CP1 — Foundation:** T1–T4 provide a compiling Go binary, domain contracts, schema, repository, FTS, and scanner port.
- **CP2 — Canonical mutation:** T5–T8 provide trust-aware atomic mutation, workspace identity, projection, retrieval, usage, and pruning.
- **CP3 — Local boundary:** T9–T12 provide keys, auth, managed files, HTTP/OpenAPI, MCP, CLI, SessionStart, import/export, review, and promotion.
- **CP4 — Recovery/lifecycle:** T13–T14 provide purge, backups, migrations, restore, maintenance locking, setup, doctor, and platform services.
- **CP5 — v0.0.1 candidate:** T15 executes every acceptance gate, final compliance mapping, two complete independent reviews, and final identity freeze.

The reconciler may split a task into smaller packets, but may not change these interfaces, ownership boundaries, or acceptance gates without a PM decision recorded against the plan.

## File and ownership map

| Area | Owner boundary | Files introduced or owned |
| --- | --- | --- |
| Bootstrap | T1 | `go.mod`, `go.sum`, `cmd/talaria-mem/main.go`, `.github/workflows/ci.yml`, `Makefile`, `.gitignore`, `internal/testutil/` |
| Domain/ports | T2 | `internal/domain/`, `internal/ports/`, including the `KeyDeriver` port |
| Canonical SQL | T3 | `db/migrations/`, `db/queries/`, `db/sqlc.yaml`, `internal/adapters/sqlite/` |
| Scanner | T4 | `internal/scanner/`, `testdata/secrets/` |
| Application | T5 | `internal/application/` mutation/trust/idempotency files |
| Workspace | T6 | `internal/workspace/` |
| Projection | T7 | `internal/projection/` |
| Retrieval | T8 | `internal/retrieval/` |
| Security/filesystem | T9 | `internal/security/`, `internal/adapters/filesystem/` |
| HTTP/MCP/CLI | T10 | `api/openapi/`, generated HTTP contract, `internal/adapters/http/`, `internal/adapters/mcp/`, `internal/cli/` |
| Codex hook | T11 | `internal/adapters/codex/`, `packaging/codex/` |
| Import/export/promotion | T12 | `internal/application/import_export.go`, `internal/application/promotion.go`, matching CLI/MCP tests and fixtures |
| Maintenance | T13 | `internal/maintenance/`, backup/purge/migration fixtures |
| Lifecycle | T14 | `internal/lifecycle/`, `packaging/macos/`, `packaging/linux/`, setup/doctor fixtures |
| Acceptance | T15 | `test/acceptance/`, `test/e2e/`, `docs/superpowers/receipts/`, final CI gates |

Shared files are owned by the reconciler. Developers must not concurrently edit `go.mod`, migration ordering, generated OpenAPI output, CI, or shared test helpers; the reconciler assigns those changes explicitly.

---

### Task 1: Bootstrap the Go module and deterministic test harness

**Dependencies:** none. **Checkpoint:** CP1. **Gate coverage:** G00, G30.

**Files:**

- Create: `go.mod`, `go.sum`, `cmd/talaria-mem/main.go`, `internal/testutil/clock.go`, `internal/testutil/fixtures.go`, `Makefile`, `.gitignore`, `.github/workflows/ci.yml`.
- Test: `cmd/talaria-mem/main_test.go`, `internal/testutil/clock_test.go`.

**Interfaces:**

- Produce `cmd/talaria-mem` with `main()` delegating to a testable `Run(ctx, args, stdout, stderr) error`.
- Produce `testutil.FixedClock` implementing `Now() time.Time` and `Advance(time.Duration)`.
- Produce Make targets `test`, `test-race`, `vet`, `generate`, `openapi-lint`, and `check` that never leave binaries in source directories.

- [ ] Write a test proving `Run` returns usage failure for an unknown command and success for `--help` without creating files.
- [ ] Run `go test ./cmd/talaria-mem ./internal/testutil -count=1`; capture the expected RED failure because the module/entry point is absent.
- [ ] Add the module and minimal command/test harness; pin all direct dependencies only when their first task needs them.
- [ ] Run the same focused tests and `go vet ./cmd/talaria-mem ./internal/testutil`; capture GREEN output.
- [ ] Add CI commands for `go test ./...`, `go test -race ./...`, `go vet ./...`, generated-output diff, Vacuum lint/bundle, and explicit output paths.
- [ ] Run `make check`; verify no generated binary or temporary file is untracked.
- [ ] Commit `chore: bootstrap talaria-mem test harness`.

### Task 2: Define domain contracts, normalization, trust, lifecycle, and ports

**Dependencies:** T1. **Checkpoint:** CP1. **Gate coverage:** G04, G08, G09, G10, G11, G12, G20, G21.

**Files:**

- Create: `internal/domain/memory.go`, `internal/domain/revision.go`, `internal/domain/workspace.go`, `internal/domain/errors.go`, `internal/domain/limits.go`, `internal/ports/repository.go`, `internal/ports/scanner.go`, `internal/ports/projector.go`, `internal/ports/clock.go`, `internal/ports/keys.go`.
- Test: matching `_test.go` files in `internal/domain/` and `internal/ports/`.

**Interfaces:**

- Define `type MemoryKind string` with `state`, `procedure`, `failure`, and `standing_instruction`.
- Define `type Trust string` with `verified` and `unverified`; `type Lifecycle string` with `active`, `quarantined`, `forgotten`, and `purged`; and failure-only `type ResolutionState string` with `open` and `resolved`.
- Define immutable `MemoryRevision`, current-pointer `Memory`, `Workspace`, `WorkspaceRedirect`, `MemoryAlias`, `Provenance`, and `RevisionRef` values using UUIDv7 string IDs and UTC nanosecond timestamps. A failure revision defaults to `open`, accepts only the versioned `ResolutionState`, and every other kind rejects that field.
- Define `NormalizeV1(kind, title, content string, tags []string) (string, error)` for valid UTF-8, NFC, CRLF/CR-to-LF conversion, stable sorted canonical tags, and exact duplicate comparison.
- Define `MemoryRepository`, `Scanner`, `Projector`, and `Clock` ports without interfaces for non-replaceable helpers. Define `KeyDeriver` for versioned, domain-separated derivation of session, idempotency, and backup-manifest keys; callers receive derived bytes only.
- Define exact shared limits: at most eight concurrent reads, one writer, a two-second FTS deadline, and a five-second mutation deadline excluding explicit maintenance operations.
- Define typed errors for validation, not-found, revision conflict, idempotency conflict, secret refusal, quarantine, timeout, unavailable, and maintenance lock failures.

- [ ] Write table tests for every enum, failure-only `ResolutionState` validation and transition preconditions, byte limit, exact concurrency/deadline limit, normalization case, duplicate-key rejection contract, `KeyDeriver` purpose separation, and error classification.
- [ ] Run `go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1' -count=1`; capture RED failures.
- [ ] Implement the smallest immutable value types and ports.
- [ ] Run `go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1' -count=1`, `go vet ./internal/domain ./internal/ports`, and a compile-only package check; capture GREEN output at `docs/superpowers/receipts/talaria-mem-v0.0.1/t2-domain-green.txt`.
- [ ] Commit `feat: define memory and workspace domain contracts`.

### Task 3: Build SQLite schema, migrations, FTS, repository, and transaction primitives

**Dependencies:** T2. **Checkpoint:** CP1. **Gate coverage:** G01, G10, G11, G12, G19, G20, G21, G30.

**Files:**

- Create: `db/migrations/000001_core.up.sql`, `db/migrations/000001_core.down.sql`, `db/migrations/000002_usage_security.up.sql`, `db/migrations/000002_usage_security.down.sql`, `db/migrations/000003_maintenance.up.sql`, `db/migrations/000003_maintenance.down.sql`, `db/queries/memory.sql`, `db/queries/workspace.sql`, `db/queries/usage.sql`, `db/queries/maintenance.sql`, `db/sqlc.yaml`, `internal/adapters/sqlite/db.go`, `internal/adapters/sqlite/repository.go`, `internal/adapters/sqlite/tx.go`.
- Generated: `internal/adapters/sqlite/sqlc/` through sqlc only.
- Test: `internal/adapters/sqlite/*_test.go`, migration fixtures under `testdata/sqlite/`.

**Interfaces:**

- Create/open database with WAL, foreign keys, `secure_delete=ON`, incremental-vacuum capability, and FTS5 `secure-delete=1`.
- Implement exactly the logical tables in specification section 8, including workspaces/bindings/redirects/aliases, memories/revisions, failure-only `resolution_state` constraints, `memory_fts`, usage, outbox, projection state, promotions, deletion receipts, purge operations, managed backups, idempotency, and migration journal.
- Implement repository methods `CreateRevision`, `MoveCurrentRevision`, `ReplaceFTSRow`, `AppendOutbox`, `ReadCurrent`, `RebuildFTS`, and `WithTx` with no independent adapter mutation path.

- [ ] Write tests for schema creation, restart persistence, failure `resolution_state` constraints, FTS eligibility predicate, transaction rollback, foreign keys, duplicate IDs, and injected SQL failure.
- [ ] Run `go test ./internal/adapters/sqlite -run 'TestSchema|TestTransaction|TestFTS' -count=1`; capture RED.
- [ ] Write migrations and sqlc queries; run migration up/down against temporary databases.
- [ ] Generate sqlc output and run focused tests plus `go test -race ./internal/adapters/sqlite`; capture GREEN.
- [ ] Verify `memory_fts` contains exactly one current active/verified/non-quarantined row and no other row.
- [ ] Commit `feat: add sqlite canonical storage and fts schema`.

### Task 4: Embed Betterleaks behind a fail-closed scanner port

**Dependencies:** T2. **Checkpoint:** CP1. **Gate coverage:** G15, G16, G30.

**Files:**

- Create: `internal/scanner/betterleaks.go`, `internal/scanner/config.go`, `internal/scanner/errors.go`, `internal/scanner/rule_upgrade.go`, reviewed pinned rule configuration under `internal/scanner/rules/`, and scanner fixtures under `testdata/secrets/`.
- Test: `internal/scanner/*_test.go`.

**Interfaces:**

- Implement `type Scanner interface { Scan(ctx context.Context, fields []TextField) ScanResult }` from T2 using Betterleaks as the only provider.
- `ScanResult` must distinguish clean, finding, uncertain, timeout, panic, cancellation, and scanner error without returning content or match fragments.
- Pin Betterleaks module and reviewed rules; disable provider validation and every network feature in code and configuration.
- Expose explicit rule-set upgrade and full active-data rescan through a scanner-owned port. Comparative fixtures gate the upgrade; readiness remains false during rescan and projection rebuild, and a failed or uncertain rescan leaves the prior rule set active. No upgrade path may enable provider validation or network access.

- [ ] Write the full boundary matrix test fixture for title, content, tags, provenance labels, source locator, read paths, export, projection, skill promotion, logs, and errors.
- [ ] Run `go test ./internal/scanner -run 'TestBoundary|TestRuleUpgrade|TestNoEgress' -count=1`; capture RED for a clean fixture, each fail-closed state, comparative rule fixture, and readiness transition.
- [ ] Implement Betterleaks adapter, panic/timeout containment, safe offsets/rule IDs, and no reversible redaction map.
- [ ] Run `go test ./internal/scanner -run 'TestBoundary|TestRuleUpgrade|TestNoEgress' -count=1` with network-disabled settings and assert canary absence from logs/errors; capture GREEN at `docs/superpowers/receipts/talaria-mem-v0.0.1/t4-scanner-green.txt`.
- [ ] Commit `feat: embed fail-closed betterleaks scanner`.

### Task 5: Implement atomic application mutations, trust transitions, review queue, and idempotency

**Dependencies:** T2, T3, T4. **Checkpoint:** CP2. **Gate coverage:** G04, G05, G06, G07, G10, G11, G12, G15, G19, G30.

**Files:**

- Create: `internal/application/memory_service.go`, `internal/application/trust.go`, `internal/application/idempotency.go`, `internal/application/review.go`, `internal/application/confirm.go`, `internal/application/forget.go`, `internal/application/restore.go`.
- Test: matching application tests and fixtures under `testdata/application/`.

**Interfaces:**

- Implement `Create`, `Update`, `Confirm`, `Pin`, `Forget`, `Restore`, `ReviewUnverified`, and `Explain` application methods accepting caller, workspace, expected revision, idempotency key, and bounded request values.
- Enforce the normative trust matrix: default-unverified writes; CLI `--verified` only; MCP/import no override; confirmation only for an exact revision; standing instructions only through CLI verified add/update.
- Enforce failure-only `resolution_state`: default `open`, expected-revision checked transitions to `resolved`, rejection for all other kinds, and inclusion of open failures in SessionStart/pruning protection.
- Perform scan, immutable revision/tombstone, current pointer, FTS, and outbox updates in one transaction; failed scan/conflict performs none.
- Store only HMAC-derived session identifiers and domain-separated idempotency digests; never store raw session IDs, query text, content, or secret fragments.
- Retain each idempotency key, operation, digest, target IDs, and safe result for exactly 24 hours; cleanup after downtime is bounded and deterministic, and replay/conflict behavior is durable across restart.

- [ ] Write tests for each actor/operation matrix cell, failure-state transition/rejection, exact-revision race, all-or-nothing scanner refusal, review pagination, idempotency replay/conflict/24-hour expiry/restart cleanup, tombstone restore, and safe metadata responses.
- [ ] Run `go test ./internal/application -run 'TestTrustMatrix|TestResolutionState|TestIdempotencyRetention|TestAtomicMutation' -count=1`; capture RED.
- [ ] Implement application orchestration using the SQLite and scanner ports.
- [ ] Run `go test ./internal/application -run 'TestTrustMatrix|TestResolutionState|TestIdempotencyRetention|TestAtomicMutation' -count=1`, race tests, and transaction rollback fixtures; capture GREEN at `docs/superpowers/receipts/talaria-mem-v0.0.1/t5-application-green.txt`.
- [ ] Commit `feat: implement atomic memory application services`.

### Task 6: Implement workspace inference, binding, normalization, merge, aliases, and redirects

**Dependencies:** T2, T3, T5. **Checkpoint:** CP2. **Gate coverage:** G08, G09, G10, G20, G30.

**Files:**

- Create: `internal/workspace/resolver.go`, `internal/workspace/binding.go`, `internal/workspace/merge.go`, `internal/workspace/receipt.go`.
- Test: `internal/workspace/*_test.go`, Git fixture repositories under `testdata/workspaces/`.

**Interfaces:**

- Implement resolution order: explicit binding; normalized credential-free `host/owner/repository`; repository-root fingerprint; absolute path fallback.
- Emit and persist the first-inference warning without silently selecting a similarly named workspace.
- Implement dry-run/apply merge receipts bound to source/target watermarks, expiry, ID/alias changes, redirects, and target reversal.
- Deduplicate only exact normalization-v1 kind/title/body/sorted-tags matches; preserve provenance/revision chains; reject ID collisions, cycles, and expired/reused receipts; schedule source and target projections transactionally.

- [ ] Write tests for every inference source, first-use warning, explicit bind, normalization matrix, duplicate/nonduplicate merge, alias/redirect lookup, collision, cycle, stale watermark, expiry, replay, and idempotence.
- [ ] Run workspace tests and capture RED.
- [ ] Implement resolver and transactionally guarded merge.
- [ ] Run workspace tests, SQLite atomicity tests, and CLI-independent receipt verification; capture GREEN.
- [ ] Commit `feat: add workspace identity and transactional merge`.

### Task 7: Implement deterministic Markdown projection and outbox worker

**Dependencies:** T3, T5. **Checkpoint:** CP2. **Gate coverage:** G10, G13, G14, G15, G30.

**Files:**

- Create: `internal/projection/format.go`, `internal/projection/render.go`, `internal/projection/worker.go`, `internal/projection/failpoints.go`.
- Test: `internal/projection/*_test.go`, projection crash/fingerprint fixtures under `testdata/projection/`.

**Interfaces:**

- Render complete active/verified/non-quarantined scope content in versioned UTF-8, LF, stable memory-ID order with machine header and fingerprint.
- Implement bounded idempotent outbox grouping by scope and revision watermark.
- Implement same-directory mode-0600 temp, fsync file, no-follow rename, fsync parent, fingerprint verification, then outbox acknowledgement.
- Handle crash after rename, stale temp cleanup, drifted state, quarantine rebuild, and explicit fingerprint-bound `projection rebuild --force`.

- [ ] Write failpoint tests for create, file fsync, rename, directory fsync, verification, acknowledgement, stale temp, drift, and scanner rebuild.
- [ ] Run failpoint tests and capture RED.
- [ ] Implement deterministic renderer and worker; inject filesystem and failpoint ports.
- [ ] Run projection tests, restart replay tests, and mode/symlink checks; capture GREEN.
- [ ] Commit `feat: add deterministic outbox markdown projection`.

### Task 8: Implement FTS retrieval, scoring, usage accounting, and pruning recommendations

**Dependencies:** T3, T5. **Checkpoint:** CP2. **Gate coverage:** G01, G02, G20, G21, G22, G30.

**Files:**

- Create: `internal/retrieval/search.go`, `internal/retrieval/ranking.go`, `internal/retrieval/usage.go`, `internal/retrieval/pruning.go`.
- Test: `internal/retrieval/*_test.go`, frozen numeric fixtures under `testdata/retrieval/`.

**Interfaces:**

- Build parameter-bound FTS5 MATCH expressions from NFC literal terms only; never accept raw FTS operators or SQL fragments.
- Implement raw BM25 weights `(5.0, 1.0, 2.0, 0.0)`, normalized relevance, freshness, usage cap, combined cap, deterministic tie-breakers, and explainable score components using injected UTC clock and float64.
- Record paired opportunities and at-most-once-per-consumer-session hits through the T2 `KeyDeriver` port; maintain 90-day detailed buckets, lifetime counts, bounded cleanup, and protect failure memories whose `resolution_state` is `open`.
- Implement the exact Wilson upper-bound formula with `z = 1.64485362695`, 30-day/20-opportunity grace, default 5% threshold, protection precedence, `--include-verified`, and forget-only apply.

- [ ] Write tests for lexical miss rescue prohibition, score math, caps, tie ordering, injected time, session deduplication, opportunity invariant, 90-day retention, Wilson fixtures, protection overrides, and explain output.
- [ ] Run `go test ./internal/retrieval -run 'TestSearch|TestUsage|TestPruning|TestResolutionProtection' -count=1`; capture RED.
- [ ] Implement retrieval and pruning services against the canonical repository.
- [ ] Run `go test ./internal/retrieval -run 'TestSearch|TestUsage|TestPruning|TestResolutionProtection' -count=1`, race tests, and deterministic repeatability checks; capture GREEN at `docs/superpowers/receipts/talaria-mem-v0.0.1/t8-retrieval-green.txt`.
- [ ] Commit `feat: add lexical retrieval usage and pruning scores`.

### Task 9: Implement root keys, HMAC subkeys, bearer authentication, and managed filesystem

**Dependencies:** T2, T3, T4. **Checkpoint:** CP3. **Gate coverage:** G15, G16, G17, G18, G19, G26, G30.

**Files:**

- Create: `internal/security/root_key.go`, `internal/security/derive.go`, `internal/security/token.go`, `internal/security/auth.go`, `internal/adapters/filesystem/managed.go`, `internal/adapters/filesystem/atomic.go`.
- Test: matching security/filesystem tests and platform-independent fixtures under `testdata/security/`.

**Interfaces:**

- Store one protected root key outside the database rollback unit; implement the T2 `KeyDeriver` port with fixed-version HKDF-SHA256 purpose subkeys for sessions, idempotency, and backup manifests.
- Keep bearer token independent; implement constant-time comparison and token-only rotation.
- Enforce literal `127.0.0.1`/`::1`, configured Host, Authorization Bearer only, Origin policy, no forwarding headers, JSON mutation content types, exact eight-reader/one-writer and two-/five-second bounds, and no generic outbound client.
- Enforce owner/mode/no-follow/no-clobber/same-directory fsync/rename/fsync semantics for all managed bytes and safe fingerprints for forced replacement.

- [ ] Write tests for key derivation separation/version, loss/corruption readiness failure, token rotation, every auth matrix, Host/Origin/query/cookie/forwarding rejection, denied egress for every daemon operation, process-specific no-`connect` proof, separate CLI/hook/MCP-client literal-loopback proof, dependency/static reachability audit, scanner-network configuration lockout, symlink/mode/owner/no-clobber behavior, and safe diagnostics.
- [ ] Run security tests and capture RED.
- [ ] Implement key, auth, and filesystem ports with no content in errors/logs.
- [ ] Run `go test ./internal/security ./internal/adapters/filesystem -run 'TestKeyDerivation|TestAuth|TestNoEgress|TestManagedFilesystem' -count=1`, race tests, process-specific socket/DNS fixtures, and dependency/static reachability checks; capture GREEN at `docs/superpowers/receipts/talaria-mem-v0.0.1/t9-security-green.txt`.
- [ ] Commit `feat: enforce local trust and managed filesystem boundaries`.

### Task 10: Implement OpenAPI, HTTP control service, MCP, and CLI contract surfaces

**Dependencies:** T5, T6, T8, T9. **Checkpoint:** CP3. **Gate coverage:** G04, G05, G06, G08, G09, G15, G17, G20, G30.

**Files:**

- Create: `api/openapi/talaria.yaml`, `api/openapi/vacuum.yaml`, `api/openapi/README.md`, `internal/adapters/http/server.go`, `internal/adapters/http/errors.go`, `internal/adapters/http/middleware.go`, generated `internal/adapters/http/openapi.gen.go`, `internal/adapters/mcp/server.go`, `internal/cli/root.go`, `internal/cli/commands/*.go`.
- Test: HTTP contract tests, MCP tool tests, CLI command tests, and `api/openapi/*_test.go` where generated contracts permit.

**Interfaces:**

- Define versioned `/control/v1/session-start`, `/healthz`, `/readyz`, and all CLI/MCP operations with UUIDv7, UTC nanoseconds, strict enums, duplicate-key rejection, 1 MiB default body limit, safe error envelope, receipt ID, and retryability.
- Generate the HTTP server/client types only after Vacuum lint/bundle; never hand-edit generated output.
- Implement MCP Streamable HTTP with `memory_search`, `memory_get`, `memory_create`, `memory_update`, `memory_pin`, `memory_forget`, and `memory_explain`.
- Implement stable CLI exit codes 0–7, machine JSON on stdout and human output on stderr, random idempotency keys by default, and no query/content logging.

- [ ] Write failing contract tests for route/method/content type/auth/error/size limits, MCP schema and workspace scope, CLI exit codes and machine output.
- [ ] Run focused tests and `vacuum lint`/bundle checks against the initial source; capture RED for missing handlers.
- [ ] Implement OpenAPI source, bundle/lint, generated code, HTTP adapters, MCP server, and Cobra command routing.
- [ ] Run generation with a clean diff, `go test ./internal/adapters/http ./internal/adapters/mcp ./internal/cli -run 'TestContract|TestMCP|TestCLI' -count=1`, `go vet`, and race tests; capture GREEN at `docs/superpowers/receipts/talaria-mem-v0.0.1/t10-surfaces-green.txt`.
- [ ] Commit `feat: expose authenticated http mcp and cli contracts`.

### Task 11: Implement Codex SessionStart and bounded context selection

**Dependencies:** T5, T6, T8, T9, T10. **Checkpoint:** CP3. **Gate coverage:** G02, G03, G08, G17, G20, G30.

**Files:**

- Create: `internal/adapters/codex/session_start.go`, `internal/adapters/codex/request.go`, `internal/adapters/codex/response.go`, `packaging/codex/session-start.sh`.
- Test: `internal/adapters/codex/*_test.go`, transcript canary fixtures under `testdata/codex/`.

**Interfaces:**

- Decode only event ID, session ID, hook name, and working directory; discard unknown fields and never open/copy/log/persist transcript-related fields.
- Resolve persisted workspace binding only; never accept event workspace override or implicit other-workspace retrieval.
- Select active/verified/non-quarantined items in pinned standing, pinned, unresolved failure, then score tiers; workspace precedes global after exact normalization-v1 deduplication.
- Enforce five-item/8 KiB pin reserves per scope, 20 whole-item/32 KiB response, 8 KiB item content, omitted-count and safe receipt, resolved workspace ID, untrusted-reference delimiters, and rescan-before-serialization fail-closed behavior.
- Install only SessionStart; hook reports actionable missing-daemon error and never downloads or replaces binaries.

- [ ] Write fixture tests for every priority/tie/dedup/reserve/bound/scanner failure/transcript canary path.
- [ ] Run hook/session tests and transcript filesystem-open canary; capture RED.
- [ ] Implement decoder, selector, serializer, and hook wrapper through the authenticated service.
- [ ] Run focused tests, race tests, and raw filesystem evidence checks; capture GREEN.
- [ ] Commit `feat: add bounded verified sessionstart context`.

### Task 12: Implement import/export, review queue, confirmation, promotion, and content-output scanning

**Dependencies:** T5, T6, T7, T8, T9, T10. **Checkpoint:** CP3. **Gate coverage:** G04, G05, G06, G07, G15, G18, G20, G30.

**Files:**

- Create: `internal/application/import_export.go`, `internal/application/promotion.go`, `internal/cli/commands/import.go`, `internal/cli/commands/export.go`, `internal/cli/commands/review.go`, `internal/cli/commands/skill.go`, `internal/adapters/mcp/tool_mutations.go`.
- Test: import/export/review/promotion/confirmation tests and fixtures under `testdata/io/`.

**Interfaces:**

- Implement versioned generated Markdown and `talaria.memory.v1` JSON Lines import/export, all-or-nothing limits of 10 MiB or 1,000 items, dry-run default, conflict failure, and safe output scanning/file replacement.
- Implement keyset review queue ordered by `(created_at, memory_id)` with 10 default/20 maximum/32 KiB whole-item bounds, freshly scanned content, provenance, revision, omitted count, and opaque cursor.
- Implement CLI exact-revision confirmation and restore; no MCP confirmation or verified override.
- Implement procedure-only skill promotion with active/verified/non-quarantined checks, dry-run/apply receipt fingerprinting, safe target output, and recorded source revision/output fingerprint.

- [ ] Write failing tests for format validation, duplicate/conflicting import, limits, unverified review discovery after lost output/restart, stale confirmation, promotion trust/fingerprint/race, and secret scanning on every output route.
- [ ] Run focused tests and capture RED.
- [ ] Implement import/export/review/promotion adapters through application services.
- [ ] Run focused tests, restart fixtures, scanner matrix, and race tests; capture GREEN.
- [ ] Commit `feat: add review import export and skill promotion flows`.

### Task 13: Implement purge, backups, migrations, restore, inventory reconciliation, and global maintenance lock

**Dependencies:** T3, T5, T7, T9. **Checkpoint:** CP4. **Gate coverage:** G11, G12, G13, G14, G15, G19, G23, G24, G25, G26, G30.

**Files:**

- Create: `internal/maintenance/lock.go`, `internal/maintenance/purge.go`, `internal/maintenance/backup.go`, `internal/maintenance/inventory.go`, `internal/maintenance/migrate.go`, `internal/maintenance/restore.go`, `internal/maintenance/readiness.go`.
- Test: maintenance tests and crash/failpoint fixtures under `testdata/maintenance/`.

**Interfaces:**

- Implement dry-run/apply, single-use expiring receipts bound to workspace/memory/revision/projection/backup inventory.
- Quiesce requests, drain readers, insert non-content `purge_pending`, remove identity/revisions/FTS/usage/outbox/idempotency/promotion links, secure-delete SQLite/FTS rows, checkpoint/truncate WAL, rebuild projections, delete inventoried matching backups, fsync, mark complete, and issue deletion receipt last.
- Implement authenticated pre-created pending backup sidecars, `VACUUM INTO`, hash/fsync/rename/complete manifest, inventory mirror, read-only integrity/foreign-key/schema/FTS/application checks, and startup reconciliation of complete/partial/orphan/unauthenticated entries.
- Apply forward-only transactional migrations with exact partial-version journal, unready state on failure, restart resume, verified backup, and no `VACUUM` inside ordinary migration.
- Implement offline restore under one global lifecycle/database lock with daemon-stop verification, sidecar identity/hash/mode/integrity checks, same-directory replacement, inventory rebuild, and startup eligibility checks.
- Run bounded, deterministic startup cleanup of idempotency rows older than the 24-hour retention window without touching newer replay/conflict records; cleanup is covered by restart and downtime fixtures.

- [ ] Write failpoint tests at every purge, backup, inventory, migration, restore, lock, idempotency-cleanup, and startup boundary before production code.
- [ ] Run failpoints and capture RED.
- [ ] Implement maintenance state machine and recovery protocol using repository/filesystem/key ports.
- [ ] Run crash-resume, concurrent daemon, denied readiness, WAL/journal, inventory, backup authentication, schema/FTS, idempotency-cleanup, and restore tests; capture GREEN at `docs/superpowers/receipts/talaria-mem-v0.0.1/t13-maintenance-green.txt`.
- [ ] Commit `feat: add crash-resumable maintenance and recovery`.

### Task 14: Implement setup, daemon readiness, doctor/status, token rotation, and platform lifecycle

**Dependencies:** T9, T10, T13. **Checkpoint:** CP4. **Gate coverage:** G17, G18, G19, G26, G27, G29, G30.

**Files:**

- Create: `internal/lifecycle/daemon.go`, `internal/lifecycle/readiness.go`, `internal/lifecycle/setup.go`, `internal/lifecycle/doctor.go`, `internal/cli/commands/setup.go`, `internal/cli/commands/status.go`, `internal/cli/commands/doctor.go`, `internal/cli/commands/token.go`, `packaging/macos/com.araihu.talaria-mem.plist`, `packaging/linux/talaria-mem.service`.
- Test: `internal/lifecycle/*_test.go`, platform-independent setup fixtures under `testdata/lifecycle/`.

**Interfaces:**

- Implement foreground/diagnostic daemon, storage/scanner/migration/projection readiness, health vs readiness distinction, bounded startup recovery, and actionable missing-daemon hook error.
- Implement `setup codex --dry-run|--apply|--remove --dry-run|--apply`, unknown configuration preservation, same-directory backup/fsync/rename/fsync, idempotence, collision refusal, rollback, and installation fingerprint removal.
- Implement `status`, `doctor`, `doctor --repair=fts --dry-run|--apply <receipt>`, `token rotate`, database/WAL/freelist sizes, ownership/mode/sidecar/unsafe path diagnostics without content. FTS repair compares row IDs and normalized content hashes, deletes/repopulates only under the global lock, and requires an explicit dry-run receipt.
- Install macOS LaunchAgent and Linux systemd-user declarations without downloading/upgrading/replacing binaries.

- [ ] Write failing lifecycle/setup tests for dry-run, idempotence, unknown config, collision, rollback, fingerprint removal, readiness blockers, token rotation, FTS repair dry-run/apply and row/hash comparison, and platform file contents.
- [ ] Run `go test ./internal/lifecycle -run 'TestSetup|TestReadiness|TestDoctorFTSRepair|TestToken|TestPlatform' -count=1`; capture RED.
- [ ] Implement lifecycle commands, service templates, and readiness wiring.
- [ ] Run `go test ./internal/lifecycle -run 'TestSetup|TestReadiness|TestDoctorFTSRepair|TestToken|TestPlatform' -count=1`, race tests, and setup fixture matrix; capture GREEN at `docs/superpowers/receipts/talaria-mem-v0.0.1/t14-lifecycle-green.txt`.
- [ ] Commit `feat: add daemon lifecycle setup and diagnostics`.

### Task 15: Integrated acceptance, compliance, final evidence, and candidate freeze

**Dependencies:** T1–T14. **Checkpoint:** CP5. **Gate coverage:** G00–G30.

**Files:**

- Create: `test/acceptance/`, `test/e2e/`, and receipt artifacts under `docs/superpowers/receipts/talaria-mem-v0.0.1/`.
- Modify: `Makefile`, `.github/workflows/ci.yml`, `README.md` only when required to document the implemented v0.0.1 journey and verified commands.

**Interfaces:**

- Bind each acceptance test to a stable gate ID, spec section, exact command, exit code, raw output location, and candidate commit/tree.
- Produce a specification-to-code traceability matrix covering sections 1–16, every included requirement, every excluded/deferred feature, and every section-14 acceptance gate.
- Produce a pre-review candidate Git identity receipt: base, HEAD, tree, branch, remote SHA, staged/unstaged/untracked status, managed branch/worktree ledgers, tests, and deferred work. Candidate receipt bytes and candidate source remain immutable after review begins.
- Produce the final review-verdict receipt in the external control-plane ledger, keyed by the candidate commit/tree and containing both independent verdicts, reviewer identities, exact evidence, and deferred work. Do not commit or alter candidate bytes after either review; this avoids a self-referential receipt and keeps one reviewed identity authoritative.
- The matrix must include named owner, exact command, negative fixture, and receipt path for: failure-only `resolution_state`; eight-reader/one-writer plus two-/five-second limits; 24-hour idempotency replay/cleanup; `doctor --repair=fts` dry-run/apply row/hash repair; Betterleaks comparative rule upgrade and full active-data rescan/readiness; and denied-egress/no-`connect`/literal-loopback/static-reachability evidence.

- [ ] Build the gate matrix from the approved spec and plan; fail the matrix if a gate lacks an executable test or evidence location.
- [ ] Run all explicit memory, restart, FTS, SessionStart, transcript canary, trust, review queue, import/MCP, promotion, workspace, merge, revision, projection, scanner, egress, auth, filesystem, key, search, usage, pruning, purge, backup, migration, lock, setup, Vacuum, generated-code, and platform gates.
- [ ] Run `go test ./...`, `go test -race ./...`, `go vet ./...`, generation diff checks, Vacuum lint/bundle, and explicit-output build checks.
- [ ] Run final specification-to-code compliance manually with exact section/file/line evidence; classify every item as implemented, intentionally excluded, or blocker.
- [ ] Commit `test: prove talaria-mem v0.0.1 acceptance gates` with all candidate source, tests, receipts, and compliance evidence before review; record its exact HEAD/tree as the sole candidate identity.
- [ ] Freeze candidate; stop writers; notify both reviewers with the complete packet and exact candidate identity.
- [ ] Address findings only through a bounded correction packet, then create a new candidate identity and rerun all affected gates and both reviews. Any byte, generated artifact, or evidence change restarts the freeze.
- [ ] Require both reviewers to return `ACCEPT` for the same candidate commit/tree; write verdicts only to the external control-plane receipt, never into the reviewed candidate.

## Acceptance gate traceability

| Gate | Requirement from specification section 14 | Task(s) |
| --- | --- | --- |
| G00 | Compiling one Go module/binary, deterministic commands, no source-tree binaries | T1, T15 |
| G01 | Explicit memory survives restart and appears in FTS | T3, T5, T8, T15 |
| G02 | SessionStart verified-only, precedence, dedup, caps, pins, scanner failure | T8, T11, T15 |
| G03 | Transcript canary and filesystem-open proof | T11, T15 |
| G04 | Actor/trust/confirmation/standing-instruction and failure `resolution_state` matrix, including 24-hour idempotency retention | T2, T5, T10, T11, T12, T15 |
| G05 | Bounded unverified review rediscovery after lost output/restart | T5, T12, T15 |
| G06 | MCP/import imperative writes remain unverified and absent from context | T5, T10, T11, T12, T15 |
| G07 | Skill promotion trust and revision/fingerprint receipt | T5, T12, T15 |
| G08 | First inferred workspace warning | T6, T11, T15 |
| G09 | Binding and transactional merge/normalization/alias/redirect matrix | T6, T10, T15 |
| G10 | Atomic revision/FTS/outbox mutation and forget/restore | T3, T5, T6, T7, T13, T15 |
| G11 | Tombstone revision, restore trust, stale conflict, restart | T5, T13, T15 |
| G12 | FTS active/verified/non-quarantined predicate across all lifecycle paths plus `doctor --repair=fts` dry-run/apply row/hash recovery | T3, T5, T7, T8, T13, T14, T15 |
| G13 | Projection failpoints, deterministic replay, drift quarantine/rebuild | T7, T13, T15 |
| G14 | Quarantine removes FTS/outbox intent and blocks readiness until scrub | T4, T5, T7, T13, T15 |
| G15 | Secret fixtures across every content boundary and safe logs/errors, including comparative rule upgrades and full active-data rescan | T4, T5, T7, T9, T12, T14, T15 |
| G16 | Denied egress for every daemon operation; process-specific no-`connect`, literal-loopback client, dependency/static reachability, and scanner-network lockout evidence | T4, T9, T15 |
| G17 | Loopback Host/Origin/credential/method/route authentication matrix | T9, T10, T14, T15 |
| G18 | Managed ownership/mode/no-follow/no-clobber | T9, T12, T14, T15 |
| G19 | Root key loss, purpose derivation, bearer-only rotation, restore manifest | T9, T13, T14, T15 |
| G20 | Search/session caps, eight-reader/one-writer and two-/five-second limits, whole-item omission, metadata limits, safe receipts | T2, T8, T9, T10, T11, T12, T15 |
| G21 | Usage/opportunity accounting and score explanations | T8, T15 |
| G22 | Pruning grace, Wilson bound, thresholds, protections, deterministic ordering | T8, T15 |
| G23 | Crash-resumable purge and complete managed representation cleanup | T5, T7, T9, T13, T15 |
| G24 | Backup sidecar failpoints, orphan/partial resume, receipt cleanup, inventory restore | T3, T9, T13, T15 |
| G25 | Migration partial versions, forward resume, verified backup, full restore, unready state | T3, T13, T15 |
| G26 | Global lock excludes daemon and offline maintenance | T9, T13, T14, T15 |
| G27 | Setup dry-run, unknown config, idempotence, collision, rollback, fingerprint removal | T9, T14, T15 |
| G28 | Vacuum OpenAPI lint/bundle and generated-code checks | T1, T10, T15 |
| G29 | macOS LaunchAgent and Linux systemd-user declared platform tests | T14, T15 |
| G30 | Full suite, race, vet, code generation, requirement-level evidence matrix, external verdict receipt, and clean immutable candidate identity | T1–T15 |

## Specification section traceability

| Specification section | Covered by |
| --- | --- |
| 1 Purpose and 2 Scope | T1–T15; exclusions frozen in Global Constraints |
| 3 Trust boundary and invariants | T2, T4, T5, T9, T10, T11, T12, T13, T15 |
| 4 Runtime architecture, interfaces, SessionStart, lifecycle | T7, T9, T10, T11, T14, T15 |
| 5 Security, Betterleaks rule upgrade/rescan, managed filesystem | T4, T9, T12, T13, T14, T15 |
| 6 Workspace identity and merge | T6, T10, T11, T15 |
| 7 Memory model, trust, failure `resolution_state`, lifecycle, purge | T2, T5, T6, T8, T12, T13, T15 |
| 8 Canonical storage, transactions, Markdown projection | T3, T5, T7, T13, T15 |
| 9 Retrieval and ranking | T8, T10, T11, T12, T15 |
| 10 Pruning | T8, T12, T15 |
| 11 SQLite operations, FTS repair, and migrations | T3, T9, T13, T14, T15 |
| 12 MCP and CLI contracts | T5, T10, T11, T12, T14, T15 |
| 13 Implementation structure | T1–T14 |
| 14 Acceptance gates | T15 plus the mapped gates above |
| 15 Rejected/deferred alternatives | T1, T15, ROADMAP.md |
| 16 Maturity receipt | T15 |

## Final self-review checklist

- [ ] Every included specification section and section-14 gate maps to a task and executable evidence.
- [ ] Every task has exact files, interfaces, dependencies, tests, RED/GREEN commands, and a commit boundary.
- [ ] No task depends on undefined types, functions, migration names, or generated artifacts.
- [ ] Shared files have an explicit reconciler owner.
- [ ] TDD, no-secret-output, no-network, optimistic revision, FTS eligibility, projection durability, and lifecycle locks are tested before implementation claims.
- [ ] Candidate receipts and evidence are committed before review; final verdicts live only in the external control-plane receipt keyed to the immutable candidate identity.
- [ ] The plan contains no `TODO`, `TBD`, “implement later,” “appropriate error handling,” or similar placeholder.
- [ ] Post-v0.0.1 roadmap work is explicitly excluded.
