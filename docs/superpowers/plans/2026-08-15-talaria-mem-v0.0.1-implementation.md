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
- Memory mutations use expected-revision checks where applicable and caller-supplied idempotency keys; content-bearing writes and content outputs are scanner-gated, and memory revision/FTS/outbox changes are atomic where specified.
- Purge is a receipt-authorized, non-content destructive operation that uses the global maintenance lock, does not require a scanner, completes when the scanner is unavailable without emitting content, and is idempotent through its receipt. Restore remains scanner-gated and, when scanning is unavailable, fails closed without emitting content or changing state.
- Token rotation, setup, diagnostics, projection repair, and maintenance use their dedicated bearer-authentication, lock, receipt, or fingerprint contracts; they do not inherit memory revision, scanner, or FTS/outbox requirements unless they handle content.
- Use TDD: write one behavior test, record a literal RED command with its expected nonzero exit and receipt, implement the minimum behavior, then record a literal GREEN command with exit `0` and receipt before refactoring.
- Never edit generated code by hand. Vacuum and bundle OpenAPI sources before oapi-codegen output.
- Preserve raw command output, exit codes, file paths, commit/tree identities, and all Git status classes in every receipt.
- Acceptance does not authorize merge, push, PR, tag, release, publish, deploy, rollback, branch deletion, worktree removal, or control-plane archival.

## Dependency DAG and checkpoint map

The table below is the authoritative direct-edge DAG. Every task header repeats
the same dependency set; T15 validates exact equality (normalizing `none` and an
empty set to the same value) and acyclicity before any checkpoint is accepted.

| Task | Direct dependencies |
| --- | --- |
| T1 | none |
| T2 | T1 |
| T3 | T2 |
| T4 | T2 |
| T5 | T2, T3, T4 |
| T6 | T2, T3, T5 |
| T7 | T2, T3, T4, T5 |
| T8 | T3, T5 |
| T9 | T2, T3, T4 |
| T10 | T5, T6, T8, T9 |
| T11 | T5, T6, T8, T9, T10 |
| T12 | T5, T6, T7, T8, T9, T10 |
| T13 | T2, T3, T4, T5, T7, T9, T10 |
| T14 | T1, T2, T3, T4, T5, T6, T7, T8, T9, T10, T11, T12, T13 |
| T15 | T1, T2, T3, T4, T5, T6, T7, T8, T9, T10, T11, T12, T13, T14 |

T2 owns the replaceable `KeyDeriver`, `ManagedFileStore`, and `ActivationJournal`
ports. `ManagedFileStore` covers mode-checked temporary creation, no-follow
replacement, file/parent fsync, stale-temp cleanup, and fingerprints.
`ActivationJournal` durably records the active and candidate rule generations,
phase (`pending`, `quiesced`, `rescanning`, `quarantining`,
`projection_rebuild`, `active`, `candidate_discarded`, `live_mutation_started`,
`failed`, or `rollback`), revision watermark, candidate rule fingerprint, last
processed ID, `live_mutation_started`, per-boundary mutation counts, resume
cursor, and safe error metadata; it never stores content. `rollback` is valid
only before `live_mutation_started`; after that boundary the journal is
monotonic and can only resume scrub/activation or remain failed and unready.
T5 and T8 depend only on `KeyDeriver` and deterministic test doubles; they never
import root-key storage or persist raw session identifiers.
T7 consumes `Scanner`, `ManagedFileStore`, and `ActivationJournal` ports and
compiles against fakes. T9 remains the sole concrete managed-filesystem
implementation and wiring boundary; it implements and wires the real root-key,
HKDF, and `ManagedFileStore` providers. T13 owns the SQLite-backed journal
adapter and cross-component rule-activation orchestration. T14 consumes journal
state for readiness. T10 consumes the T8 retrieval service explicitly; no
surface task may stub or duplicate search, usage, pruning, or explain behavior.

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
| Bootstrap | T1 | `go.mod`, `go.sum`, stable `cmd/talaria-mem/main.go` `Run` shell, `.github/workflows/ci.yml`, `Makefile`, `.gitignore`, `internal/testutil/` |
| Domain/ports | T2 | `internal/domain/`, `internal/ports/`, including `KeyDeriver`, `ManagedFileStore`, and `ActivationJournal` ports |
| Canonical SQL | T3 | `db/migrations/`, `db/queries/`, `db/sqlc.yaml`, `internal/adapters/sqlite/` |
| Scanner | T4 | `internal/scanner/`, `testdata/secrets/` |
| Application | T5 | `internal/application/` mutation/trust/idempotency files |
| Workspace | T6 | `internal/workspace/` |
| Projection | T7 | `internal/projection/` |
| Retrieval | T8 | `internal/retrieval/` |
| Security/filesystem | T9 | `internal/security/`, `internal/adapters/filesystem/` |
| HTTP/MCP/CLI | T10 | `api/openapi/`, generated HTTP contract, `internal/adapters/http/`, `internal/adapters/mcp/`, `internal/cli/root.go`, `internal/cli/registry.go`, `internal/cli/memory_core.go`, `internal/cli/workspace.go`, `internal/cli/projection.go`, and their contract tests |
| Codex hook | T11 | `internal/adapters/codex/`, `packaging/codex/` |
| Import/export/promotion | T12 | `internal/application/import_export.go`, `internal/application/promotion.go`, matching CLI/MCP tests and fixtures |
| Maintenance | T13 | `internal/maintenance/`, `internal/cli/commands/scanner.go`, `internal/cli/commands/backup.go`, `internal/cli/commands/db.go`, backup/purge/migration/rule-upgrade fixtures; T13 owns unit/effect tests, not the composed-binary command receipt |
| Lifecycle | T14 | `internal/lifecycle/`, `internal/runtime/composition.go`, `internal/runtime/composition_test.go`, final wiring in `cmd/talaria-mem/main.go`, `internal/cli/commands/daemon.go`, `internal/cli/commands/setup.go`, `internal/cli/commands/status.go`, `internal/cli/commands/doctor.go`, `internal/cli/commands/token.go`, `packaging/macos/`, `packaging/linux/`, setup/doctor/black-box maintenance fixtures |
| Acceptance | T15 | `test/acceptance/`, `test/e2e/`, `docs/superpowers/receipts/`, final CI gates |

Shared files are owned by the reconciler. Developers must not concurrently edit `go.mod`, migration ordering, generated OpenAPI output, CI, shared test helpers, or the composition root. T10 owns only the listed CLI registry/core/workspace/projection files; T12 owns `import.go`, `export.go`, `review.go`, and `skill.go`; T13 owns `scanner.go`, `backup.go`, and `db.go`; T14 owns `daemon.go`, `setup.go`, `status.go`, `doctor.go`, and `token.go`. T1 creates the stable `Run` shell; T14 owns the final composition patch and wiring tests in the same `cmd/talaria-mem/main.go` file through an explicit reconciler packet.

---

### Task 1: Bootstrap the Go module and deterministic test harness

**Dependencies:** none. **Checkpoint:** CP1. **Gate coverage:** G00, G28, G30.

**Files:**

- Create: `go.mod`, `go.sum`, `cmd/talaria-mem/main.go`, `internal/testutil/clock.go`, `internal/testutil/fixtures.go`, `Makefile`, `.gitignore`, `.github/workflows/ci.yml`.
- Test: `cmd/talaria-mem/main_test.go`, `internal/testutil/clock_test.go`.

**Interfaces:**

- Produce `cmd/talaria-mem` with `main()` delegating to a testable `Run(ctx, args, stdout, stderr) error`.
- Keep the bootstrap `Run` shell free of provider construction; T14 owns final one-binary composition and replaces the shell's dependency wiring through the reconciler-owned composition packet.
- Produce `testutil.FixedClock` implementing `Now() time.Time` and `Advance(time.Duration)`.
- Produce Make targets `test`, `test-race`, `vet`, `generate`, `openapi-lint`, and `check` that never leave binaries in source directories.

- [ ] Write a test proving `Run` returns usage failure for an unknown command and success for `--help` without creating files.
- [ ] RED: run `go test ./cmd/talaria-mem ./internal/testutil -count=1`; expected exit `1`; negative fixture `testdata/bootstrap/missing-entrypoint.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t1-bootstrap-red.txt`.
- [ ] Add the module and minimal command/test harness; pin all direct dependencies only when their first task needs them.
- [ ] GREEN: run `go test ./cmd/talaria-mem ./internal/testutil -count=1 && go vet ./cmd/talaria-mem ./internal/testutil`; expected exit `0`; negative fixture `testdata/bootstrap/unknown-command.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t1-bootstrap-green.txt`.
- [ ] Add CI commands for `go test ./...`, `go test -race ./...`, `go vet ./...`, generated-output diff, Vacuum lint/bundle, and explicit output paths.
- [ ] Verify `make check`; expected exit `0`; negative fixture `testdata/bootstrap/untracked-binary-or-temp.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t1-check-green.txt`; assert no generated binary or temporary file is untracked.
- [ ] Commit `chore: bootstrap talaria-mem test harness`.

### Task 2: Define domain contracts, normalization, trust, lifecycle, and ports

**Dependencies:** T1. **Checkpoint:** CP1. **Gate coverage:** G04, G08, G09, G10, G11, G12, G13, G18, G19, G20, G21, G30.

**Files:**

- Create: `internal/domain/memory.go`, `internal/domain/revision.go`, `internal/domain/workspace.go`, `internal/domain/errors.go`, `internal/domain/limits.go`, `internal/ports/repository.go`, `internal/ports/scanner.go`, `internal/ports/projector.go`, `internal/ports/clock.go`, `internal/ports/keys.go`, `internal/ports/filesystem.go`, `internal/ports/activation.go`.
- Test: matching `_test.go` files in `internal/domain/` and `internal/ports/`.

**Interfaces:**

- Define `type MemoryKind string` with `state`, `procedure`, `failure`, and `standing_instruction`.
- Define `type Trust string` with `verified` and `unverified`; `type Lifecycle string` with `active`, `quarantined`, `forgotten`, and `purged`; and failure-only `type ResolutionState string` with `open` and `resolved`.
- Define immutable `MemoryRevision`, current-pointer `Memory`, `Workspace`, `WorkspaceRedirect`, `MemoryAlias`, `Provenance`, and `RevisionRef` values using UUIDv7 string IDs and UTC nanosecond timestamps. A failure revision defaults to `open`, accepts only the versioned `ResolutionState`, and every other kind rejects that field.
- Define `NormalizeV1(kind, title, content string, tags []string) (string, error)` for valid UTF-8, NFC, CRLF/CR-to-LF conversion, stable sorted canonical tags, and exact duplicate comparison.
- Define `MemoryRepository`, `Scanner`, `Projector`, and `Clock` ports without interfaces for non-replaceable helpers. Define `KeyDeriver` for versioned, domain-separated derivation of session, idempotency, and backup-manifest keys; callers receive derived bytes only.
- Define replaceable `ManagedFileStore` operations for mode-0600 temporary files, no-follow same-directory replacement, file/parent fsync, stale-temp cleanup, and safe fingerprints. Define replaceable `ActivationJournal` operations for durable rule-generation phase transitions, revision watermarks, candidate fingerprints, last processed IDs, `live_mutation_started`, per-boundary quarantine/FTS/outbox/projection counts, safe errors, monotonic resume, and readiness blockers; journal records contain no content and forbid rollback after the first live-data mutation.
- Define the canonical FTS contract constant `FTS5Tokenizer = "unicode61 remove_diacritics 2"` and require secure-delete for the FTS5 table; T3 owns the schema, while T8 and T14 consume this contract without duplicating it.
- Define exact shared limits: at most eight concurrent reads, one writer, a two-second FTS deadline, and a five-second mutation deadline excluding explicit maintenance operations.
- Define typed errors for validation, not-found, revision conflict, idempotency conflict, secret refusal, quarantine, timeout, unavailable, storage-full, and maintenance lock failures. `SQLITE_FULL` maps to `storage-full` as an ordinary safe write failure: return a stable diagnostic, perform no content output or partial mutation, and never retry indefinitely.

- [ ] Write table tests for every enum, failure-only `ResolutionState` validation and transition preconditions, byte limit, exact concurrency/deadline limit, normalization case, duplicate-key rejection contract, `KeyDeriver` purpose separation, `ManagedFileStore` no-follow/fsync contract, `ActivationJournal` phase contract, canonical `FTSConfig`/Unicode-diacritic behavior, typed `SQLITE_FULL` mapping, and error classification.
- [ ] RED: run `go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1|TestManagedFileStore|TestActivationJournal|TestFTSConfig|TestSQLiteFull' -count=1`; expected exit `1`; negative fixture `testdata/domain/missing-port-contract.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t2-domain-red.txt`.
- [ ] Implement the smallest immutable value types and ports.
- [ ] GREEN: run `go test ./internal/domain ./internal/ports -run 'TestResolutionState|TestLimits|TestKeyDeriver|TestNormalizeV1|TestManagedFileStore|TestActivationJournal|TestFTSConfig|TestSQLiteFull' -count=1 && go vet ./internal/domain ./internal/ports`; expected exit `0`; negative fixture `testdata/domain/unsafe-follow-or-invalid-phase.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t2-domain-green.txt`.
- [ ] Commit `feat: define memory and workspace domain contracts`.

### Task 3: Build SQLite schema, migrations, FTS, repository, and transaction primitives

**Dependencies:** T2. **Checkpoint:** CP1. **Gate coverage:** G01, G10, G11, G12, G19, G20, G21, G24, G25, G30.

**Files:**

- Create: `db/migrations/000001_core.up.sql`, `db/migrations/000001_core.down.sql`, `db/migrations/000002_usage_security.up.sql`, `db/migrations/000002_usage_security.down.sql`, `db/migrations/000003_maintenance.up.sql`, `db/migrations/000003_maintenance.down.sql`, `db/queries/memory.sql`, `db/queries/workspace.sql`, `db/queries/usage.sql`, `db/queries/maintenance.sql`, `db/sqlc.yaml`, `internal/adapters/sqlite/db.go`, `internal/adapters/sqlite/repository.go`, `internal/adapters/sqlite/tx.go`.
- Generated: `internal/adapters/sqlite/sqlc/` through sqlc only.
- Test: `internal/adapters/sqlite/*_test.go`, migration fixtures under `testdata/sqlite/`.

**Interfaces:**

- Create/open database with WAL, foreign keys, `secure_delete=ON`, incremental-vacuum capability, and a normal-content FTS5 table whose exact tokenizer is `tokenize='unicode61 remove_diacritics 2'` and whose FTS5 `secure-delete=1` option is enabled. Any schema/rebuild/migration drift from this contract is a hard failure.
- Implement exactly the logical tables in specification section 8, including workspaces/bindings/redirects/aliases, memories/revisions, failure-only `resolution_state` constraints, `memory_fts`, usage, outbox, projection state, promotions, deletion receipts, `purge_operations` extended for non-content backup-reconcile operation/receipt-consumption records and monotonic pre/post-effect phases, managed backups, idempotency, migration journal, and a non-content rule-upgrade activation journal containing active/candidate generations, phase, watermark, fingerprint, live-mutation flag/counts, resume cursor, and safe error metadata.
- Implement repository methods `CreateRevision`, `MoveCurrentRevision`, `ReplaceFTSRow`, `AppendOutbox`, `ReadCurrent`, `RebuildFTS`, and `WithTx` with no independent adapter mutation path. Map modernc SQLite `SQLITE_FULL` to the typed storage-full error, commit no partial revision/FTS/outbox change, emit no content, and return without an unbounded retry loop.

- [ ] Write tests for schema creation, restart persistence, failure `resolution_state` constraints, exact FTS5 tokenizer/secure-delete options, NFC/Unicode and diacritic retrieval fixtures, FTS eligibility predicate, transaction rollback, foreign keys, duplicate IDs, injected SQL failure, and deterministic `SQLITE_FULL` ordinary-write failure/no-retry behavior.
- [ ] RED: run `go test ./internal/adapters/sqlite -run 'TestSchema|TestTransaction|TestFTS|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestActivationJournal' -count=1`; expected exit `1`; negative fixture `testdata/sqlite/invalid-rule-generation-transition.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t3-sqlite-red.txt`.
- [ ] RED: write migrations and sqlc queries, then run `go test ./internal/adapters/sqlite -run 'TestMigrationRoundTrip' -count=1` against temporary databases; expected exit `1`; negative fixture `testdata/sqlite/partial-migration-v4.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t3-migration-red.txt`.
- [ ] GREEN: generate sqlc output and run `go test ./internal/adapters/sqlite -run 'TestSchema|TestTransaction|TestFTS|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestActivationJournal' -count=1 && go test -race ./internal/adapters/sqlite`; expected exit `0`; negative fixture `testdata/sqlite/fts-eligibility-drift.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t3-sqlite-green.txt`.
- [ ] Verify `memory_fts` contains exactly one current active/verified/non-quarantined row and no other row.
- [ ] Commit `feat: add sqlite canonical storage and fts schema`.

### Task 4: Embed Betterleaks behind a fail-closed scanner port

**Dependencies:** T2. **Checkpoint:** CP1. **Gate coverage:** G14, G15, G16, G30.

**Files:**

- Create: `internal/scanner/betterleaks.go`, `internal/scanner/config.go`, `internal/scanner/errors.go`, `internal/scanner/rule_upgrade.go`, reviewed pinned rule configuration under `internal/scanner/rules/`, and scanner fixtures under `testdata/secrets/`.
- Test: `internal/scanner/*_test.go`.

**Interfaces:**

- Implement `type Scanner interface { Scan(ctx context.Context, fields []TextField) ScanResult }` from T2 using Betterleaks as the only provider.
- `ScanResult` must distinguish clean, finding, uncertain, timeout, panic, cancellation, and scanner error without returning content or match fragments.
- Pin Betterleaks module and reviewed rules; disable provider validation and every network feature in code and configuration.
- Expose candidate rule loading, comparative fixtures, and scanning through a scanner-owned port; T13 owns the cross-component upgrade command, activation journal, full active-data rescan, quarantine/FTS/outbox mutation, projection scrub, and readiness transition. T4 never activates a rule set or mutates storage. No upgrade path may enable provider validation or network access.

- [ ] Write the full boundary matrix test fixture for title, content, tags, provenance labels, source locator, read paths, export, projection, skill promotion, logs, and errors.
- [ ] RED: run `go test ./internal/scanner -run 'TestBoundary|TestRuleUpgrade|TestNoEgress' -count=1`; expected exit `1`; negative fixture `testdata/secrets/rule-upgrade-uncertain.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t4-scanner-red.txt`.
- [ ] Implement Betterleaks adapter, panic/timeout containment, safe offsets/rule IDs, and no reversible redaction map.
- [ ] GREEN: run `TALARIA_SCANNER_NETWORK=disabled go test ./internal/scanner -run 'TestBoundary|TestRuleUpgrade|TestNoEgress' -count=1`; expected exit `0`; negative fixture `testdata/secrets/canary-in-log-or-error.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t4-scanner-green.txt`.
- [ ] Commit `feat: embed fail-closed betterleaks scanner`.

### Task 5: Implement atomic application mutations, trust transitions, review queue, and idempotency

**Dependencies:** T2, T3, T4. **Checkpoint:** CP2. **Gate coverage:** G01, G04, G05, G06, G07, G10, G11, G12, G14, G15, G19, G20, G23, G30.

**Files:**

- Create: `internal/application/memory_service.go`, `internal/application/trust.go`, `internal/application/idempotency.go`, `internal/application/review.go`, `internal/application/confirm.go`, `internal/application/forget.go`, `internal/application/restore.go`, `internal/application/content_output.go`, `internal/application/quarantine.go`.
- Test: matching application tests and fixtures under `testdata/application/`.

**Interfaces:**

- Implement `Create`, `Update`, `Confirm`, `Pin`, `Forget`, `Restore`, `ReviewUnverified`, and `Explain` application methods accepting caller, workspace, expected revision, idempotency key, and bounded request values.
- Enforce the normative trust matrix: default-unverified writes; CLI `--verified` only; MCP/import no override; confirmation only for an exact revision; standing instructions only through CLI verified add/update.
- Enforce failure-only `resolution_state`: default `open`, expected-revision checked transitions to `resolved`, rejection for all other kinds, and inclusion of open failures in SessionStart/pruning protection.
- Enforce `REQ-12-PIN-ELIGIBILITY` and pin reserves atomically in the mutation transaction: `Pin` accepts only the expected current revision that is active, verified, and non-quarantined; reject forgotten, purged, quarantined, unverified, stale-revision, and concurrent-race attempts. Allow no more than five pinned standing instructions or 8 KiB of pinned standing-instruction content in either workspace or user-global scope; reject item and byte overflow, including concurrent pin races. T11 retains readiness-false detection for inconsistent legacy state.
- Perform scan, immutable revision/tombstone, current pointer, FTS, and outbox updates in one transaction; failed scan/conflict performs none.
- Own `ContentOutputGuard` and `ScanAndQuarantine` as the read-time boundary. Every content-output call supplies route, workspace, item, current revision, and rule generation. A clean scan returns bounded content; a finding atomically marks the exact revision quarantined, removes its FTS row, appends outbox intent, and blocks readiness/projection until scrub/rebuild. Scanner error, timeout, panic, cancellation, or uncertainty returns no content and performs no mutation. The operation is idempotent and race-safe across concurrent revision updates and restart/failpoint recovery; no route may implement a second guard.
- Store only HMAC-derived session identifiers and domain-separated idempotency digests; never store raw session IDs, query text, content, or secret fragments.
- Retain each idempotency key, operation, digest, target IDs, and safe result for exactly 24 hours; cleanup after downtime is bounded and deterministic, and replay/conflict behavior is durable across restart.

- [ ] Write tests for each actor/operation matrix cell, failure-state transition/rejection, exact-revision race, all-or-nothing scanner refusal, review pagination, idempotency replay/conflict/24-hour expiry/restart cleanup, tombstone restore, safe metadata responses, `TestREQ_12_PinEligibility` with negative lifecycle/stale-revision/unverified/quarantined and concurrent-reserve fixtures, and `ContentOutputGuard`/`ScanAndQuarantine` route-wide finding/error behavior. Include concurrent update, projection scrub, restart, and failpoint fixtures proving atomic quarantine/FTS removal/outbox append/readiness blocking.
- [ ] RED: run `go test ./internal/application -run 'TestTrustMatrix|TestResolutionState|TestIdempotencyRetention|TestAtomicMutation|TestREQ_12_PinEligibility|TestPinReserve|TestContentOutputGuard|TestScanAndQuarantine|TestReadFindingQuarantine' -count=1`; expected exit `1`; negative fixture `testdata/application/pin-ineligible-lifecycle-or-stale-revision.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t5-application-red.txt`.
- [ ] Implement application orchestration using the SQLite and scanner ports.
- [ ] GREEN: run `go test ./internal/application -run 'TestTrustMatrix|TestResolutionState|TestIdempotencyRetention|TestAtomicMutation|TestREQ_12_PinEligibility|TestPinReserve|TestContentOutputGuard|TestScanAndQuarantine|TestReadFindingQuarantine|TestScannerFailureNoMutation' -count=1 && go test -race ./internal/application`; expected exit `0`; negative fixture `testdata/application/read-scanner-failure-no-mutation.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t5-application-green.txt`.
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
- Deduplicate only exact normalization-v1 kind/title/body/sorted-tags matches; preserve provenance/revision chains; retain conflicting non-identical items and persist a `REQ-6.1-MERGE-REVIEW` review flag/receipt rather than silently deduplicating; reject ID collisions, cycles, and expired/reused receipts; schedule source and target projections transactionally.

- [ ] Write tests for every inference source, first-use warning, explicit bind, normalization matrix, duplicate/nonduplicate merge, `TestREQ_6_1_MergeNonIdenticalReview` review flag, alias/redirect lookup, collision, cycle, stale watermark, expiry, replay, and idempotence.
- [ ] RED: run `go test ./internal/workspace -run 'TestInference|TestMerge|TestREQ_6_1_MergeNonIdenticalReview|TestReceipt' -count=1`; expected exit `1`; negative fixture `testdata/workspaces/non-identical-merge-review-flag.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t6-workspace-red.txt`.
- [ ] Implement resolver and transactionally guarded merge.
- [ ] GREEN: run `go test ./internal/workspace ./internal/adapters/sqlite -run 'TestInference|TestMerge|TestREQ_6_1_MergeNonIdenticalReview|TestReceipt|TestTransaction' -count=1`; expected exit `0`; negative fixture `testdata/workspaces/redirect-cycle.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t6-workspace-green.txt`.
- [ ] Commit `feat: add workspace identity and transactional merge`.

### Task 7: Implement deterministic Markdown projection and outbox worker

**Dependencies:** T2, T3, T4, T5. **Checkpoint:** CP2. **Gate coverage:** G10, G12, G13, G14, G15, G18, G23, G30.

**Files:**

- Create: `internal/projection/format.go`, `internal/projection/render.go`, `internal/projection/worker.go`, `internal/projection/failpoints.go`.
- Test: `internal/projection/*_test.go`, projection crash/fingerprint fixtures under `testdata/projection/`.

**Interfaces:**

- Render complete active/verified/non-quarantined scope content in versioned UTF-8, LF, stable memory-ID order with machine header and fingerprint. Call T5 `ContentOutputGuard`/`ScanAndQuarantine` for every content-bearing projection output before creating or replacing a projection file; clean content may proceed, findings are atomically quarantined with FTS removal/outbox intent, and scanner failure or uncertainty writes no content or mutation.
- Implement a per-scope projector lock and bounded idempotent outbox grouping by scope and revision watermark. Persist `attempt_count`, `next_attempt_at`, and a sanitized error for every failed event; retries are deterministic and never expose content.
- Consume the T2 `ManagedFileStore` port for mode-0600 temp, same-directory no-follow replacement, file fsync, parent fsync, and fingerprint verification; report projection-rebuild phases through the T2 `ActivationJournal`; acknowledge outbox only after all checks succeed. T7 uses compiling filesystem/scanner/journal fakes and does not import T9 concrete filesystem code.
- Handle crash after rename, stale temp cleanup, drifted state, quarantine rebuild, and explicit fingerprint-bound `projection rebuild --force`.

- [ ] Write failpoint tests for create, file fsync, rename, directory fsync, verification, acknowledgement, stale temp, drift, scanner rebuild, T5 guard invocation, finding quarantine, concurrent revision update, restart recovery, projection scrub/readiness blocking, and `TestREQ_8_2_ProjectionRetryMetadata` covering the per-scope lock, `attempt_count`, `next_attempt_at`, and sanitized error.
- [ ] RED: run `go test ./internal/projection -run 'TestProjectionFailpoints|TestScanBeforeWrite|TestOutboxAck|TestREQ_8_2_ProjectionRetryMetadata|TestContentOutputGuard|TestReadFindingQuarantine' -count=1`; expected exit `1`; negative fixture `testdata/projection/retry-metadata-or-scope-lock.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t7-projection-red.txt`.
- [ ] Implement deterministic renderer and worker; inject filesystem and failpoint ports.
- [ ] GREEN: run `go test ./internal/projection -run 'TestProjectionFailpoints|TestScanBeforeWrite|TestOutboxAck|TestREQ_8_2_ProjectionRetryMetadata|TestRestartReplay|TestContentOutputGuard|TestReadFindingQuarantine|TestScannerFailureNoMutation' -count=1 && go test -race ./internal/projection`; expected exit `0`; negative fixture `testdata/projection/symlink-target.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t7-projection-green.txt`.
- [ ] Commit `feat: add deterministic outbox markdown projection`.

### Task 8: Implement FTS retrieval, scoring, usage accounting, and pruning recommendations

**Dependencies:** T3, T5. **Checkpoint:** CP2. **Gate coverage:** G01, G02, G12, G20, G21, G22, G30.

**Files:**

- Create: `internal/retrieval/search.go`, `internal/retrieval/ranking.go`, `internal/retrieval/usage.go`, `internal/retrieval/pruning.go`.
- Test: `internal/retrieval/*_test.go`, frozen numeric fixtures under `testdata/retrieval/`.

**Interfaces:**

- Build parameter-bound FTS5 MATCH expressions from NFC literal terms only; never accept raw FTS operators or SQL fragments.
- Consume T2's exact `FTS5Tokenizer = "unicode61 remove_diacritics 2"` contract and test Unicode/diacritic-equivalent terms, schema rebuild, and migration preservation; retrieval must never silently fall back to another tokenizer.
- Implement raw BM25 weights `(5.0, 1.0, 2.0, 0.0)`, normalized relevance, freshness, usage cap, combined cap, deterministic tie-breakers, and explainable score components using injected UTC clock and float64.
- Record paired opportunities and at-most-once-per-consumer-session hits through the T2 `KeyDeriver` port; maintain 90-day detailed buckets, lifetime counts, and `REQ-8.9-USAGE-CLEANUP` cleanup at startup, on the daily bounded task, and as deterministic catch-up after downtime; protect failure memories whose `resolution_state` is `open`.
- Implement the exact Wilson upper-bound formula with `z = 1.64485362695`, 30-day/20-opportunity grace, default 5% threshold, protection precedence, `--include-verified`, forget-only apply, and `REQ-10-PRUNING-TOP20`: search opportunities use only eligible positive-raw-relevance candidates in the top 20 before any freshness or usage boost, with stable raw-relevance tie-breakers.

- [ ] Write tests for lexical miss rescue prohibition, score math, caps, tie ordering, injected time, session deduplication, opportunity invariant, `TestREQ_8_9_UsageCleanupStartupDailyDowntime`, 90-day retention, Wilson fixtures, `TestREQ_10_PruningTop20PreBoost`, protection overrides, explain output, and Unicode/diacritic retrieval plus FTS rebuild preservation.
- [ ] RED: run `go test ./internal/retrieval -run 'TestSearch|TestUsage|TestREQ_8_9_UsageCleanupStartupDailyDowntime|TestPruning|TestREQ_10_PruningTop20PreBoost|TestResolutionProtection|TestUnicodeDiacritic|TestFTSRebuild' -count=1`; expected exit `1`; negative fixture `testdata/retrieval/usage-cleanup-downtime-or-pruning-top20.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t8-retrieval-red.txt`.
- [ ] Implement retrieval and pruning services against the canonical repository.
- [ ] GREEN: run `go test ./internal/retrieval -run 'TestSearch|TestUsage|TestREQ_8_9_UsageCleanupStartupDailyDowntime|TestPruning|TestREQ_10_PruningTop20PreBoost|TestResolutionProtection|TestUnicodeDiacritic|TestFTSRebuild' -count=1 && go test -race ./internal/retrieval`; expected exit `0`; negative fixture `testdata/retrieval/session-dedup-restart.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t8-retrieval-green.txt`.
- [ ] Commit `feat: add lexical retrieval usage and pruning scores`.

### Task 9: Implement root keys, HMAC subkeys, bearer authentication, and managed filesystem

**Dependencies:** T2, T3, T4. **Checkpoint:** CP3. **Gate coverage:** G15, G16, G17, G18, G19, G20, G23, G24, G26, G30.

**Files:**

- Create: `internal/security/root_key.go`, `internal/security/derive.go`, `internal/security/token.go`, `internal/security/auth.go`, `internal/adapters/filesystem/managed.go`, `internal/adapters/filesystem/atomic.go`.
- Test: matching security/filesystem tests and platform-independent fixtures under `testdata/security/`.

**Interfaces:**

- Store one protected root key outside the database rollback unit; implement the T2 `KeyDeriver` port with fixed-version HKDF-SHA256 purpose subkeys for sessions, idempotency, and backup manifests.
- Implement the concrete T2 `ManagedFileStore` in `internal/adapters/filesystem/` and expose it only through the port; T9 is the filesystem implementation and composition/wiring boundary used by T7, T13, and T14.
- Keep bearer token independent; implement constant-time comparison and token-only rotation.
- Enforce literal `127.0.0.1`/`::1`, configured Host, Authorization Bearer only, Origin policy, no forwarding headers, JSON mutation content types, exact eight-reader/one-writer and two-/five-second bounds, and no generic outbound client.
- Enforce owner/mode/no-follow/no-clobber/same-directory fsync/rename/fsync semantics for all managed bytes and safe fingerprints for forced replacement.

- [ ] Write tests for key derivation separation/version, loss/corruption readiness failure, token rotation, every auth matrix, Host/Origin/query/cookie/forwarding rejection, denied egress for every daemon operation, process-specific no-`connect` proof, separate CLI/hook/MCP-client literal-loopback proof, dependency/static reachability audit, scanner-network configuration lockout, symlink/mode/owner/no-clobber behavior, and safe diagnostics.
- [ ] RED: run `go test ./internal/security ./internal/adapters/filesystem -run 'TestKeyDerivation|TestAuth|TestNoEgress|TestManagedFilesystem' -count=1`; expected exit `1`; negative fixture `testdata/security/egress-connect.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t9-security-red.txt`.
- [ ] Implement key, auth, and filesystem ports with no content in errors/logs.
- [ ] GREEN: run `go test ./internal/security ./internal/adapters/filesystem -run 'TestKeyDerivation|TestAuth|TestNoEgress|TestManagedFilesystem' -count=1 && go test -race ./internal/security ./internal/adapters/filesystem`; expected exit `0`; negative fixture `testdata/security/symlink-target.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t9-security-green.txt`.
- [ ] Commit `feat: enforce local trust and managed filesystem boundaries`.

### Task 10: Implement OpenAPI, HTTP control service, MCP, and CLI contract surfaces

**Dependencies:** T5, T6, T8, T9. **Checkpoint:** CP3. **Gate coverage:** G04, G05, G06, G08, G09, G14, G15, G17, G20, G28, G30.

**Files:**

- Create: `api/openapi/talaria.yaml`, `api/openapi/vacuum.yaml`, `api/openapi/README.md`, `internal/adapters/http/server.go`, `internal/adapters/http/errors.go`, `internal/adapters/http/middleware.go`, generated `internal/adapters/http/openapi.gen.go`, `internal/adapters/mcp/server.go`, `internal/cli/root.go`, `internal/cli/registry.go`, `internal/cli/memory_core.go`, `internal/cli/workspace.go`, and `internal/cli/projection.go`. T10 does not own any wildcard under `internal/cli/commands/`: T12 owns `import.go`, `export.go`, `review.go`, and `skill.go`; T13 owns `scanner.go`, `backup.go`, and `db.go`; T14 owns `daemon.go`, `setup.go`, `status.go`, `doctor.go`, and `token.go`.
- Test: HTTP contract tests, MCP tool tests, CLI command tests, and `api/openapi/*_test.go` where generated contracts permit.

**Interfaces:**

- Define versioned `/control/v1/session-start`, `/healthz`, `/readyz`, and all CLI/MCP operations with UUIDv7, UTC nanoseconds, strict enums, duplicate-key rejection, 1 MiB default body limit, safe error envelope, receipt ID, and retryability.
- Generate the HTTP server/client types only after Vacuum lint/bundle; never hand-edit generated output.
- Implement MCP Streamable HTTP with `memory_search`, `memory_get`, `memory_create`, `memory_update`, `memory_pin`, `memory_forget`, and `memory_explain`.
- Implement stable CLI exit codes 0–7, machine JSON on stdout and human output on stderr, random idempotency keys by default, and no query/content logging. The ordinary CLI uses an authenticated loopback client and never opens SQLite; offline maintenance is limited to the explicitly owned T13/T14 commands and global lock.
- Route every HTTP, MCP, and CLI content-output path through T5 `ContentOutputGuard` before serialization or projection handoff. A clean scan is the only path that returns content; a finding invokes T5 `ScanAndQuarantine` atomically and blocks readiness/projection, while scanner error or uncertainty returns no content and performs no mutation. T10 exposes the stable registration boundary for T13's maintenance commands but does not own their implementation.

- [ ] Write failing contract tests for route/method/content type/auth/error/size limits, MCP schema and workspace scope, CLI exit codes and machine output, and every HTTP/MCP/CLI content-output guard/finding/error route.
- [ ] RED: run `vacuum lint api/openapi/talaria.yaml api/openapi/vacuum.yaml && vacuum bundle api/openapi/talaria.yaml`; expected exit `1`; negative fixture `testdata/surfaces/invalid-origin.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t10-surfaces-red.txt`.
- [ ] RED: run `go test ./internal/adapters/http ./internal/adapters/mcp ./internal/cli -run 'TestContract|TestMCP|TestCLI|TestContentOutputGuard|TestRouteQuarantine|TestScannerFailureNoMutation' -count=1`; expected exit `1`; negative fixture `testdata/surfaces/missing-handler-or-invalid-mcp-schema.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t10-contract-red.txt`.
- [ ] Implement OpenAPI source, bundle/lint, generated code, HTTP adapters, MCP server, and Cobra command routing.
- [ ] GREEN: run `vacuum lint api/openapi/talaria.yaml api/openapi/vacuum.yaml && vacuum bundle api/openapi/talaria.yaml && go generate ./... && git diff --exit-code -- internal/adapters/http/openapi.gen.go && go test ./internal/adapters/http ./internal/adapters/mcp ./internal/cli -run 'TestContract|TestMCP|TestCLI|TestContentOutputGuard|TestRouteQuarantine|TestScannerFailureNoMutation' -count=1 && go vet ./... && go test -race ./internal/adapters/http ./internal/adapters/mcp ./internal/cli`; expected exit `0`; negative fixture `testdata/surfaces/duplicate-json-key.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t10-surfaces-green.txt`.
- [ ] Commit `feat: expose authenticated http mcp and cli contracts`.

### Task 11: Implement Codex SessionStart and bounded context selection

**Dependencies:** T5, T6, T8, T9, T10. **Checkpoint:** CP3. **Gate coverage:** G02, G03, G04, G06, G08, G14, G15, G17, G20, G30.

**Files:**

- Create: `internal/adapters/codex/session_start.go`, `internal/adapters/codex/request.go`, `internal/adapters/codex/response.go`, `packaging/codex/session-start.sh`.
- Test: `internal/adapters/codex/*_test.go`, transcript canary fixtures under `testdata/codex/`.

**Interfaces:**

- Decode only event ID, session ID, hook name, and working directory; discard unknown fields and never open/copy/log/persist transcript-related fields.
- Resolve persisted workspace binding only; never accept event workspace override or implicit other-workspace retrieval.
- Select active/verified/non-quarantined items in pinned standing, pinned, unresolved failure, then score tiers; workspace precedes global after exact normalization-v1 deduplication.
- Enforce five-item/8 KiB pin reserves per scope, 20 whole-item/32 KiB response, 8 KiB item content, omitted-count and safe receipt, resolved workspace ID, untrusted-reference delimiters, and rescan-before-serialization fail-closed behavior. Detect any inconsistent legacy pin state and keep readiness false rather than silently omitting a pinned instruction; new pin writes are rejected atomically by T5.
- Route every SessionStart content output through T5 `ContentOutputGuard`; a finding calls `ScanAndQuarantine` atomically (quarantine revision, remove FTS, append outbox intent, block readiness/projection), while scanner error or uncertainty returns no content and performs no mutation. Include concurrent update, restart, and failpoint evidence for the read-time boundary.
- Install only SessionStart; hook reports actionable missing-daemon error and never downloads or replaces binaries.

- [ ] Write fixture tests for every priority/tie/dedup/reserve/bound/scanner failure/transcript canary path, plus read-finding quarantine and scanner-failure/no-mutation route cases.
- [ ] RED: run `go test ./internal/adapters/codex -run 'TestSessionStart|TestTranscriptCanary|TestSelection' -count=1`; expected exit `1`; negative fixture `testdata/codex/transcript-open-attempt.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t11-session-red.txt`.
- [ ] Implement decoder, selector, serializer, and hook wrapper through the authenticated service.
- [ ] GREEN: run `go test ./internal/adapters/codex -run 'TestSessionStart|TestTranscriptCanary|TestSelection|TestTranscriptFilesystemOpen|TestReadFindingQuarantine|TestScannerFailureNoMutation' -count=1 && go test -race ./internal/adapters/codex`; expected exit `0`; negative fixture `testdata/codex/scanner-finding-or-uncertain-no-content.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t11-session-green.txt`.
- [ ] Commit `feat: add bounded verified sessionstart context`.

### Task 12: Implement import/export, review queue, confirmation, promotion, and content-output scanning

**Dependencies:** T5, T6, T7, T8, T9, T10. **Checkpoint:** CP3. **Gate coverage:** G04, G05, G06, G07, G14, G15, G18, G20, G30.

**Files:**

- Create: `internal/application/import_export.go`, `internal/application/promotion.go`, `internal/cli/commands/import.go`, `internal/cli/commands/export.go`, `internal/cli/commands/review.go`, `internal/cli/commands/skill.go`, `internal/adapters/mcp/tool_mutations.go`.
- Test: import/export/review/promotion/confirmation tests and fixtures under `testdata/io/`.

**Interfaces:**

- Implement versioned generated Markdown and `talaria.memory.v1` JSON Lines import/export, all-or-nothing limits of 10 MiB or 1,000 items, dry-run default, conflict failure, and safe output scanning/file replacement.
- Implement keyset review queue ordered by `(created_at, memory_id)` with 10 default/20 maximum/32 KiB whole-item bounds, freshly scanned content, provenance, revision, omitted count, and opaque cursor.
- Implement CLI exact-revision confirmation and restore; no MCP confirmation or verified override.
- Implement procedure-only skill promotion with active/verified/non-quarantined checks, dry-run/apply receipt fingerprinting, safe target output, and recorded source revision/output fingerprint.
- Route every import/export/review/confirmation/promotion and other content-output path through T5 `ContentOutputGuard`; clean content is returned only after a scanner success, findings invoke atomic `ScanAndQuarantine` and block readiness/projection, and scanner error/uncertainty returns no content and no mutation.

- [ ] Write failing tests for format validation, duplicate/conflicting import, limits, unverified review discovery after lost output/restart, stale confirmation, promotion trust/fingerprint/race, and secret scanning on every output route, including read-finding quarantine and scanner-failure/no-mutation failpoints.
- [ ] RED: run `go test ./internal/application ./internal/adapters/mcp ./internal/cli -run 'TestImport|TestExport|TestReview|TestPromotion|TestContentOutputScan|TestRouteQuarantine|TestScannerFailureNoMutation' -count=1`; expected exit `1`; negative fixture `testdata/io/lost-output-restart.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t12-io-red.txt`.
- [ ] Implement import/export/review/promotion adapters through application services.
- [ ] GREEN: run `go test ./internal/application ./internal/adapters/mcp ./internal/cli -run 'TestImport|TestExport|TestReview|TestPromotion|TestContentOutputScan|TestRouteQuarantine|TestScannerFailureNoMutation' -count=1 && go test -race ./internal/application ./internal/adapters/mcp ./internal/cli`; expected exit `0`; negative fixture `testdata/io/secret-in-export-or-promotion.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t12-io-green.txt`.
- [ ] Commit `feat: add review import export and skill promotion flows`.

### Task 13: Implement purge, backups, migrations, restore, rule activation, inventory reconciliation, and global maintenance lock

**Dependencies:** T2, T3, T4, T5, T7, T9, T10. **Checkpoint:** CP4. **Gate coverage:** G10, G11, G12, G13, G14, G15, G19, G23, G24, G25, G26, G30.

**Files:**

- Create: `internal/maintenance/lock.go`, `internal/maintenance/purge.go`, `internal/maintenance/backup.go`, `internal/maintenance/reconcile.go`, `internal/maintenance/inventory.go`, `internal/maintenance/migrate.go`, `internal/maintenance/restore.go`, `internal/maintenance/rule_upgrade.go`, `internal/maintenance/activation_journal.go`, `internal/maintenance/readiness.go`, `internal/cli/commands/scanner.go`, `internal/cli/commands/backup.go`, and `internal/cli/commands/db.go` for operator-owned maintenance commands. T13 owns package/unit/effect evidence; the composed-binary backup command receipt belongs to T14 after composition.
- Test: maintenance tests and crash/failpoint fixtures under `testdata/maintenance/`.

**Interfaces:**

- Implement dry-run/apply, single-use expiring receipts bound to workspace/memory/revision/projection/backup inventory.
- Quiesce requests, drain readers, insert non-content `purge_pending`, remove identity/revisions/FTS/usage/outbox/idempotency/promotion links, secure-delete SQLite/FTS rows, checkpoint/truncate WAL, rebuild projections, delete inventoried matching backups, fsync, mark complete, and issue deletion receipt last.
- Implement authenticated pre-created pending backup sidecars, `VACUUM INTO`, hash/fsync/rename/complete manifest, inventory mirror, read-only integrity/foreign-key/schema/FTS/application checks, and startup reconciliation of complete/partial/orphan/unauthenticated entries. Startup records unknown entries as managed cleanup candidates, keeps readiness false, and performs no destructive action.
- Own the literal operator commands `talaria-mem db backup reconcile --dry-run` and `talaria-mem db backup reconcile --apply <receipt>` in the T13 maintenance package and `db.go` registration. Dry-run writes a single-use, expiring receipt bound to the exact path, owner/mode, content fingerprint/hash, proposed action, scope, and receipt ID. Apply first persists a non-content operation and receipt-consumption record (reusing or extending `purge_operations`) before any effect, then requires the unchanged path/owner/mode/fingerprint/action, unexpired unused receipt, and global maintenance lock. Its monotonic phases are `pre_effect`, `quarantine_renamed`, `file_fsynced`, `deleted`, `inventory_rebuilt`, and `receipt_complete`; each phase is idempotently resumed and cannot replay a completed receipt authority. A crash before the first effect leaves the entry untouched and the operation resumable; a crash after any effect converges to the recorded quarantine/rename/fsync/delete/inventory/readiness state and never treats the receipt as reusable. Drift, expiry, or replay refuses without content output. T10 owns only authenticated client registration; T14 owns the composed-binary black-box evidence.
- Apply forward-only transactional migrations with exact partial-version journal, unready state on failure, restart resume, verified backup, and no `VACUUM` inside ordinary migration.
- Implement offline restore under one global lifecycle/database lock with daemon-stop verification, sidecar identity/hash/mode/integrity checks, same-directory replacement, inventory rebuild, and startup eligibility checks.
- Run bounded, deterministic startup cleanup of idempotency rows older than the 24-hour retention window without touching newer replay/conflict records; cleanup is covered by restart and downtime fixtures.
- Own `talaria-mem scanner rules upgrade --candidate <rule-version> --dry-run` and `talaria-mem scanner rules upgrade --apply <receipt>`. Bind the receipt to the active rule generation, candidate fingerprint, database revision watermark, and expiry; no rule upgrade runs implicitly.
- Implement the cross-component Betterleaks activation state machine through the T2 `ActivationJournal`: persist `pending` before acquiring the global maintenance lock, quiesce and drain every memory writer (no watermark-only alternative), run comparative fixtures and a full active-data rescan while writers remain excluded, and persist a fixed candidate watermark. Before any live-data mutation, a scanner/comparative failure or crash may transition to `candidate_discarded`, discard the candidate generation, restore the prior active generation, and restore readiness. At the first candidate finding/quarantine/FTS/outbox/projection mutation, atomically persist `live_mutation_started=true` and boundary counts; after that point the protocol is monotonic and must never undo quarantine, suppress a finding, roll back the candidate rules, or re-expose candidate-detected content. Keep readiness false, durably scrub and rebuild projections through the T7 ports, resume from the journal after restart, and atomically mark the candidate `active` only after all counts, fixed watermark, projection fingerprint, and readiness checks pass.
- On scanner failure, uncertainty, panic, cancellation, timeout, projection failure, or crash before `live_mutation_started`, discard the candidate and restore the prior active generation. After `live_mutation_started`, persist `failed` or the next monotonic phase, retain every quarantine/FTS-removal/outbox/projection mutation, resume scrub/rebuild/activation under the lock, and keep all content-output routes closed; if recovery cannot complete, remain failed and unready. Add crash fixtures after comparative scan, first quarantine, FTS removal, outbox append, each projection scrub/rename/fsync, activation CAS, and restart; no route may return content during any pending, mutation-started, failed, or uncertain phase. T13 is the only owner of this orchestration; T4 remains scanner-local and T9 remains the concrete managed-filesystem boundary.
- Purge is receipt/lock-authorized and non-content: it must complete without a scanner and emit no content. Restore remains receipt/lock-authorized and scanner-gated; a scanner outage rejects restore without output or state change. Add explicit operation-matrix evidence for both paths.

- [ ] Write failpoint tests at every purge, backup, inventory, migration, restore, rule-upgrade phase, lock, idempotency-cleanup, and startup boundary before production code. Include concurrent writer and scanner-outage fixtures, unknown/orphan/unauthenticated no-delete startup cases, dry-run/apply receipt drift/expiry/single-use cases, `TestRuleUpgradeMonotonicRecovery` and crash fixtures at every pre/post-live-mutation boundary, `TestBackupReconcilePreEffectCrash`, `TestBackupReconcilePostEffectCrash`, and receipt-consumption convergence evidence.
- [ ] RED: run `go test ./internal/maintenance -run 'TestRuleUpgrade|TestRuleUpgradeFailpoint|TestRuleUpgradeMonotonicRecovery|TestRuleUpgradeConcurrentWriter|TestRuleUpgradeRestart|TestRuleUpgradeReadiness|TestPurge|TestBackup|TestBackupReconcile|TestBackupReconcilePreEffectCrash|TestBackupReconcilePostEffectCrash|TestUnknownBackupNoDelete|TestBackupReceiptDrift|TestInventory|TestMigration|TestRestore|TestMaintenanceLock|TestIdempotencyCleanup' -count=1`; expected exit `1`; negative fixture `testdata/maintenance/rule-upgrade-live-mutation-crash-or-backup-reconcile-post-effect.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t13-maintenance-red.txt`.
- [ ] Implement maintenance state machines and recovery protocols using repository, `ManagedFileStore`, `ActivationJournal`, scanner, projector, key, and readiness ports.
- [ ] GREEN: run `go test ./internal/maintenance -run 'TestRuleUpgrade|TestRuleUpgradeFailpoint|TestRuleUpgradeMonotonicRecovery|TestRuleUpgradeConcurrentWriter|TestRuleUpgradeRestart|TestRuleUpgradeReadiness|TestPurge|TestPurgeScannerUnavailable|TestBackup|TestBackupReconcile|TestBackupReconcilePreEffectCrash|TestBackupReconcilePostEffectCrash|TestUnknownBackupNoDelete|TestBackupReceiptDrift|TestInventory|TestMigration|TestRestore|TestRestoreScannerUnavailable|TestMaintenanceLock|TestIdempotencyCleanup' -count=1 && go test -race ./internal/maintenance`; expected exit `0`; negative fixture `testdata/maintenance/unknown-backup-no-delete.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t13-maintenance-green.txt`.
- [ ] Rule-upgrade evidence: run `go test ./internal/maintenance -run 'TestRuleUpgradeFailpoint|TestRuleUpgradeMonotonicRecovery|TestRuleUpgradeConcurrentWriter|TestRuleUpgradeRestart|TestRuleUpgradeReadiness|TestRuleUpgradeNoContentDuringRecovery' -count=1`; expected exit `0`; negative fixture `testdata/maintenance/rule-upgrade-crash-after-each-mutation-boundary.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t13-rule-upgrade-green.txt`.
- [ ] Backup-reconcile recovery evidence: run `go test ./internal/maintenance -run 'TestBackupReconcilePreEffectCrash|TestBackupReconcilePostEffectCrash|TestBackupReconcileResumeEachPhase|TestBackupReconcileReceiptSingleUse' -count=1`; expected exit `0`; negative fixture `testdata/maintenance/backup-reconcile-effect-boundary.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t13-backup-reconcile-recovery-green.txt`.
- [ ] Backup-reconcile package/effect evidence: run `go test ./internal/maintenance ./internal/cli -run 'TestBackupReconcile|TestUnknownBackupNoDelete|TestBackupReceiptDrift' -count=1`; expected exit `0`; negative fixture `testdata/maintenance/backup-receipt-drift-or-replay.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t13-backup-reconcile-green.txt`; retain the non-content operation journal, receipt-consumption phase, owner/fingerprint/action checks, lock result, and single-use convergence. T14 alone retains composed-binary dry-run/apply command output.
- [ ] Commit `feat: add crash-resumable maintenance and recovery`.

### Task 14: Implement setup, daemon readiness, doctor/status, token rotation, and platform lifecycle

**Dependencies:** T1, T2, T3, T4, T5, T6, T7, T8, T9, T10, T11, T12, T13. **Checkpoint:** CP4. **Gate coverage:** G00, G12, G14, G15, G17, G18, G19, G24, G26, G27, G29, G30.

**Files:**

- Create: `internal/lifecycle/daemon.go`, `internal/lifecycle/readiness.go`, `internal/lifecycle/setup.go`, `internal/lifecycle/doctor.go`, `internal/runtime/composition.go`, `cmd/talaria-mem/main.go` final wiring, `internal/cli/commands/daemon.go`, `internal/cli/commands/setup.go`, `internal/cli/commands/status.go`, `internal/cli/commands/doctor.go`, `internal/cli/commands/token.go`, `packaging/macos/com.araihu.talaria-mem.plist`, `packaging/linux/talaria-mem.service`.
- Test: `internal/lifecycle/*_test.go`, `internal/runtime/composition_test.go`, final binary wiring tests, and platform-independent setup fixtures under `testdata/lifecycle/`.

**Interfaces:**

- Implement foreground/diagnostic daemon, storage/scanner/migration/projection readiness, health vs readiness distinction, bounded startup recovery, and actionable missing-daemon hook error. Read T13's `ActivationJournal`; readiness remains false for every pending/rescanning/quarantining/projection-rebuild/rollback/failed rule-generation phase and returns true only after durable active activation and projection verification.
- Own the final one-binary composition root: `internal/runtime/composition.go` constructs exactly one daemon, CLI, MCP, HTTP, scanner, repository, projector, lifecycle, and maintenance graph from explicit dependencies; final `cmd/talaria-mem/main.go` wiring delegates to it. No package-init registration or second binary path is allowed.
- Implement `setup codex --dry-run|--apply|--remove --dry-run|--apply`, unknown configuration preservation, same-directory backup/fsync/rename/fsync, idempotence, collision refusal, rollback, and installation fingerprint removal.
- Implement `status`, `doctor`, `doctor --repair=fts --dry-run|--apply <receipt>`, `token rotate`, database/WAL/freelist sizes, ownership/mode/sidecar/unsafe path diagnostics without content. FTS repair compares row IDs and normalized content hashes, verifies the T2 `unicode61 remove_diacritics 2` contract, deletes/repopulates only under the global lock, and requires an explicit dry-run receipt. Surface typed `storage-full` diagnostics from ordinary writes without retry loops or content output.
- Install macOS LaunchAgent and Linux systemd-user declarations without downloading/upgrading/replacing binaries.
- After the composition root exists, own the only black-box `talaria-mem db backup reconcile` evidence. Build the current source into an explicit absolute temporary binary, record its SHA-256, and run both commands against isolated fixture state/configuration and an absolute binary path; the T13 package tests remain the unit/effect evidence.

- [ ] Write failing lifecycle/setup tests for dry-run, idempotence, unknown config, collision, rollback, fingerprint removal, readiness blockers, token rotation, FTS repair dry-run/apply and row/hash comparison, exact tokenizer/Unicode preservation, typed `SQLITE_FULL` ordinary-write failure/no retry, composition-root graph cardinality, and platform file contents.
- [ ] RED: run `go test ./internal/lifecycle ./internal/runtime ./cmd/talaria-mem -run 'TestSetup|TestReadiness|TestDoctorFTSRepair|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestToken|TestPlatform|TestActivationReadiness|TestComposition|TestSingleBinary' -count=1`; expected exit `1`; negative fixture `testdata/lifecycle/missing-composition-provider.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t14-lifecycle-red.txt`.
- [ ] Implement lifecycle commands, service templates, and readiness wiring.
- [ ] GREEN: run `go test ./internal/lifecycle ./internal/runtime ./cmd/talaria-mem -run 'TestSetup|TestReadiness|TestDoctorFTSRepair|TestFTSTokenizer|TestUnicodeDiacritic|TestSQLiteFull|TestToken|TestPlatform|TestActivationReadiness|TestComposition|TestSingleBinary' -count=1 && go test -race ./internal/lifecycle ./internal/runtime ./cmd/talaria-mem`; expected exit `0`; negative fixture `testdata/lifecycle/unsafe-path-or-stale-rule-journal.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t14-lifecycle-green.txt`.
- [ ] Integrated backup-reconcile black-box evidence after composition: run `sh -eu -c 'tmpdir="$(mktemp -d /tmp/talaria-mem-reconcile.XXXXXX)"; state="$tmpdir/state"; config="$tmpdir/config"; bin="$tmpdir/talaria-mem"; mkdir -p "$state/backups" "$state/fixtures" "$config"; cp testdata/lifecycle/unknown-backup-no-delete-black-box.json "$state/backups/unknown.json"; cp testdata/lifecycle/backup-reconcile-post-effect-crash-or-replay.json "$state/fixtures/post-effect.json"; go build -o "$bin" ./cmd/talaria-mem; printf "binary="; realpath "$bin"; shasum -a 256 "$bin"; TALARIA_STATE_DIR="$state" TALARIA_CONFIG_DIR="$config" TALARIA_BACKUP_DIR="$state/backups" "$bin" db backup reconcile --dry-run >"$tmpdir/dry-run.out"; TALARIA_STATE_DIR="$state" TALARIA_CONFIG_DIR="$config" TALARIA_BACKUP_DIR="$state/backups" "$bin" db backup reconcile --apply "$state/receipts/t14-backup-reconcile-dry-run.json" >"$tmpdir/apply.out"; cat "$tmpdir/dry-run.out" "$tmpdir/apply.out"'`; expected exit `0`; negative fixtures `testdata/lifecycle/unknown-backup-no-delete-black-box.json` and `testdata/lifecycle/backup-reconcile-post-effect-crash-or-replay.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t14-backup-reconcile-black-box.json`; retain both command outputs, absolute binary path, SHA-256, isolated state/config paths, receipt path, owner/mode/fingerprint, proposed action, expiry, lock, and single-use result in the single receipt.
- [ ] Commit `feat: add daemon lifecycle setup and diagnostics`.

### Task 15: Integrated acceptance, compliance, final evidence, and candidate freeze

**Dependencies:** T1, T2, T3, T4, T5, T6, T7, T8, T9, T10, T11, T12, T13, T14. **Checkpoint:** CP5. **Gate coverage:** G00–G30.

**Files:**

- Create: `test/acceptance/`, `test/e2e/`, and receipt artifacts under `docs/superpowers/receipts/talaria-mem-v0.0.1/`.
- Modify: `Makefile`, `.github/workflows/ci.yml`, `README.md` only when required to document the implemented v0.0.1 journey and verified commands.
- Test: `test/acceptance/gate_matrix_test.go`, `test/acceptance/dag_header_test.go`, `test/acceptance/requirement_traceability_test.go`, machine-validated gate matrix, DAG/task-header, gate-header, and requirement-level bidirectional traceability tests, end-to-end journey, scanner-outage purge/restore fixtures, and specification traceability tests under `test/acceptance/` and `test/e2e/`.

**Interfaces:**

- Bind each acceptance test to a stable gate ID, stable requirement-level IDs where applicable, spec section, exact command, expected exit code, negative fixture, raw output location, receipt hash, and `tested_source_commit`/`tested_source_tree` captured before that receipt commit. The C0 evidence rows and C1 external verification rows each identify the exact tree they tested; no committed matrix row or receipt may contain the final candidate commit/tree that includes the row or receipt itself.
- Produce a specification-to-code traceability matrix covering sections 1–16, every included requirement, every excluded/deferred feature, and every section-14 acceptance gate.
- Use the exact two-phase candidate protocol. First commit all executable source, tests, generators, CI/Makefile inputs, and the matrix schema as clean baseline `C0`; freeze `C0` and execute the literal `command`, expected exit, negative fixture, and receipt path from each G00–G30 matrix row, plus the literal suite commands below, against that exact tree, capturing raw receipts and pre/post status externally. Then commit only the identity-free evidence bundle `E` (the first-run matrix rows and raw receipts, with `tested_source_commit=C0` and no final-candidate fields); the final candidate is `C1=E`. Freeze clean `C1` and execute each G00–G30 row command again, plus the literal full-suite, race/vet, generation-diff, Vacuum, DAG/header, gate/header, and specification-traceability commands below, against exact `C1`, capturing all second-run raw receipts and pre/post status externally. No bytes may change after the C1 run. The external control-plane ledger alone binds `C1`, the C1 matrix/receipt hashes, base/tree/status/manifest, and review packet. The pre-review identity receipt is external-only and never committed under the candidate.
- Produce the final review-verdict receipt only in the external control-plane ledger, keyed by `C1` and the externally captured C1 matrix/receipt hashes and containing both independent verdicts, reviewer identities, exact evidence, and deferred work. Do not commit or alter candidate bytes after the C1 run or either review; this avoids a self-referential receipt and keeps one reviewed identity authoritative.
- The matrix is a machine-validated table with required fields `gate_id|owner|spec_section|command|expected_exit|negative_fixture|receipt_path|tested_source_commit|tested_source_tree|receipt_hash`; it must include these fields for every G00–G30 row and fail closed when any field is empty or a command/fixture/receipt is missing. It must reject `candidate_commit` and `candidate_tree` fields in committed matrix/receipt artifacts; only the external Phase-B manifest may bind final candidate identity. It specifically covers failure-only `resolution_state`; atomic pin reserves; eight-reader/one-writer plus two-/five-second limits; 24-hour idempotency replay/cleanup; `doctor --repair=fts` dry-run/apply row/hash repair; Betterleaks comparative rule upgrade, durable activation journal, full active-data rescan/readiness, crash rollback, and scanner-outage purge/restore behavior; and denied-egress/no-`connect`/literal-loopback/static-reachability evidence.

- [ ] RED: run `go test ./test/acceptance -run 'TestGateMatrix|TestGateMatrixCompleteness|TestDAGHeaderTraceability|TestGateHeaderTraceability' -count=1`; expected exit `1`; negative fixture `testdata/acceptance/missing-command-or-receipt-field.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t15-matrix-red.txt`.
- [ ] GREEN: run `go test ./test/acceptance -run 'TestGateMatrix|TestGateMatrixCompleteness|TestDAGHeaderTraceability|TestGateHeaderTraceability|TestRequirementTraceability' -count=1`; expected exit `0`; negative fixture `testdata/acceptance/empty-negative-fixture.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t15-matrix-green.txt`. The test must fail if any G00–G30 row lacks `gate_id`, owner, spec section, literal command, expected exit, negative fixture, receipt path, tested source commit/tree, or receipt hash, if a committed row/receipt contains a final candidate identity, or if any stable requirement ID is missing from its owner task, named test, or gate row.
- [ ] GREEN: run `go test ./test/acceptance -run 'TestAllGates|TestJourney|TestRuleUpgrade|TestPurgeScannerOutage|TestRestoreScannerOutage' -count=1`; expected exit `0`; negative fixture `testdata/acceptance/deferred-feature-or-content-leak.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t15-gates-green.txt`.
- [ ] GREEN: run `go test ./... && go test -race ./... && go vet ./... && go generate ./... && git diff --exit-code -- internal/adapters/http/openapi.gen.go && vacuum lint api/openapi/talaria.yaml api/openapi/vacuum.yaml && vacuum bundle api/openapi/talaria.yaml`; expected exit `0`; negative fixture `testdata/acceptance/generated-diff-or-vacuum-invalid.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t15-suite-green.txt`.
- [ ] GREEN: run `go test ./test/acceptance -run 'TestSpecTraceability' -count=1`; expected exit `0`; negative fixture `testdata/acceptance/unmapped-spec-requirement.json`; receipt `docs/superpowers/receipts/talaria-mem-v0.0.1/t15-compliance-green.txt`; classify every requirement as implemented, intentionally excluded, or blocker with exact section/file/line evidence.
- [ ] Commit `C0` with all executable source/tests/generators/CI/Makefile/matrix-schema bytes, execute every G00–G30 row's literal command and the literal suite commands above on clean `C0`, then commit `E` as the identity-free evidence-only bundle and define `C1=E`; execute those same literal row/suite commands on clean `C1` with external raw receipts, preserving pre/post status and never writing `C1` into committed artifacts.
- [ ] Phase B: stop writers, verify clean `C1` HEAD/tree/status/manifest and the no-bytes-after-C1-run freeze, then write `C1`, external C1 matrix hash, receipt hashes, and review packet only to the external control-plane ledger; notify both reviewers with that external packet and exact identity.
- [ ] Address findings only through a bounded correction packet, then create a new candidate identity and rerun all affected gates and both reviews. Any byte, generated artifact, or evidence change restarts the freeze.
- [ ] Require both reviewers to return `ACCEPT` for the same candidate commit/tree; write verdicts only to the external control-plane receipt, never into the reviewed candidate.

## Acceptance gate traceability

| Gate | Requirement from specification section 14 | Task(s) |
| --- | --- | --- |
| G00 | Compiling one Go module/binary, deterministic commands, no source-tree binaries | T1, T14, T15 |
| G01 | Explicit memory survives restart and appears in FTS | T3, T5, T8, T15 |
| G02 | SessionStart verified-only, precedence, dedup, caps, pins, scanner failure | T8, T11, T15 |
| G03 | Transcript canary and filesystem-open proof | T11, T15 |
| G04 | Actor/trust/confirmation/standing-instruction and failure `resolution_state` matrix, including 24-hour idempotency retention | T2, T5, T10, T11, T12, T15 |
| G05 | Bounded unverified review rediscovery after lost output/restart | T5, T10, T12, T15 |
| G06 | MCP/import imperative writes remain unverified and absent from context | T5, T10, T11, T12, T15 |
| G07 | Skill promotion trust and revision/fingerprint receipt | T5, T12, T15 |
| G08 | First inferred workspace warning | T2, T6, T10, T11, T15 |
| G09 | Binding and transactional merge/normalization/alias/redirect matrix, including `REQ-6.1-MERGE-REVIEW` non-identical conflict review flag | T2, T6, T10, T15 |
| G10 | Atomic revision/FTS/outbox mutation and forget/restore | T2, T3, T5, T6, T7, T13, T15 |
| G11 | Tombstone revision, restore trust, stale conflict, restart | T2, T3, T5, T13, T15 |
| G12 | FTS active/verified/non-quarantined predicate with exact `unicode61 remove_diacritics 2` tokenizer and typed `SQLITE_FULL` ordinary-write failure across all lifecycle paths plus `doctor --repair=fts` dry-run/apply row/hash recovery | T2, T3, T5, T7, T8, T13, T14, T15 |
| G13 | Projection failpoints, deterministic replay, drift quarantine/rebuild, `REQ-8.2-PROJECTION-RETRY` per-scope lock and retry metadata, and rule-generation activation journal recovery | T2, T7, T13, T15 |
| G14 | Read-time and mutation-time quarantine removes FTS/outbox intent atomically and blocks readiness until scrub, including every output route and rule-upgrade activation | T4, T5, T7, T10, T11, T12, T13, T14, T15 |
| G15 | Secret fixtures across every content boundary and safe logs/errors, including comparative rule upgrades, durable activation, full active-data rescan, crash rollback, and scanner-outage operation matrix | T4, T5, T7, T9, T10, T11, T12, T13, T14, T15 |
| G16 | Denied egress for every daemon operation; process-specific no-`connect`, literal-loopback client, dependency/static reachability, and scanner-network lockout evidence | T4, T9, T15 |
| G17 | Loopback Host/Origin/credential/method/route authentication matrix | T9, T10, T11, T14, T15 |
| G18 | Managed ownership/mode/no-follow/no-clobber | T2, T7, T9, T12, T14, T15 |
| G19 | Root key loss, purpose derivation, bearer-only rotation, restore manifest | T2, T3, T5, T9, T13, T14, T15 |
| G20 | Search/session caps, `REQ-12-PIN-ELIGIBILITY` active/verified/non-quarantined pin eligibility, atomic five-item/8-KiB pin reserves, eight-reader/one-writer and two-/five-second limits, whole-item omission, metadata limits, safe receipts | T2, T3, T5, T6, T8, T9, T10, T11, T12, T15 |
| G21 | `REQ-8.9-USAGE-CLEANUP` startup/daily/downtime-catch-up usage cleanup, usage/opportunity accounting, and score explanations | T2, T3, T8, T15 |
| G22 | `REQ-10-PRUNING-TOP20` top-20 pre-boost opportunity semantics, pruning grace, Wilson bound, thresholds, protections, deterministic ordering | T8, T15 |
| G23 | Crash-resumable purge and complete managed representation cleanup | T5, T7, T9, T13, T15 |
| G24 | Backup sidecar failpoints, orphan/partial/unknown resume, no-delete startup reconciliation, explicit `db backup reconcile --dry-run`/`--apply <receipt>` binding, pre/post-effect receipt journal convergence, receipt cleanup, inventory restore | T3, T9, T13, T14, T15 |
| G25 | Migration partial versions, forward resume, verified backup, full restore, unready state | T3, T13, T15 |
| G26 | Global lock excludes daemon and offline maintenance | T9, T13, T14, T15 |
| G27 | Setup dry-run, unknown config, idempotence, collision, rollback, fingerprint removal | T14, T15 |
| G28 | Vacuum OpenAPI lint/bundle and generated-code checks | T1, T10, T15 |
| G29 | macOS LaunchAgent and Linux systemd-user declared platform tests | T14, T15 |
| G30 | Full suite, race, vet, code generation, machine-validated owner/section/command/exit/fixture/receipt/tested-source matrix, DAG/header traceability, external Phase-B identity/verdict manifest, and clean immutable candidate identity | T1, T2, T3, T4, T5, T6, T7, T8, T9, T10, T11, T12, T13, T14, T15 |

## Requirement-level traceability

The IDs below are stable requirement identifiers. The acceptance test must fail
closed unless every row appears in its owner task interface and RED/GREEN test
commands, in this table, and in the named gate row; the same test must reject an
orphan task/test/gate reference in the reverse direction.

| Requirement ID | Specification section | Owner task | Named test/evidence | Gate |
| --- | --- | --- | --- | --- |
| `REQ-6.1-MERGE-REVIEW` | §6.1 Workspace merge | T6 | `TestREQ_6_1_MergeNonIdenticalReview` / `testdata/workspaces/non-identical-merge-review-flag.json` | G09 |
| `REQ-8.2-PROJECTION-RETRY` | §8.2 Markdown projection | T7 | `TestREQ_8_2_ProjectionRetryMetadata` / `testdata/projection/retry-metadata-or-scope-lock.json` | G13 |
| `REQ-8.9-USAGE-CLEANUP` | §§8–9 Usage retention and accounting | T8 | `TestREQ_8_9_UsageCleanupStartupDailyDowntime` / `testdata/retrieval/usage-cleanup-downtime-or-pruning-top20.json` | G21 |
| `REQ-10-PRUNING-TOP20` | §10 Pruning recommendations | T8 | `TestREQ_10_PruningTop20PreBoost` / `testdata/retrieval/usage-cleanup-downtime-or-pruning-top20.json` | G22 |
| `REQ-12-PIN-ELIGIBILITY` | §§4.2 and 12 SessionStart/pin and MCP/CLI contracts | T5 | `TestREQ_12_PinEligibility` / `testdata/application/pin-ineligible-lifecycle-or-stale-revision.json` | G20 |

## Specification section traceability

| Specification section | Covered by |
| --- | --- |
| 1 Purpose and 2 Scope | T1–T15; exclusions frozen in Global Constraints |
| 3 Trust boundary and invariants | T2, T4, T5, T9, T10, T11, T12, T13, T15 |
| 4 Runtime architecture, interfaces, SessionStart, lifecycle | T2, T7, T9, T10, T11, T13, T14, T15 |
| 5 Security, Betterleaks rule upgrade/rescan/activation, managed filesystem | T2, T4, T7, T9, T12, T13, T14, T15 |
| 6 Workspace identity and merge | T6, T10, T11, T15 |
| 7 Memory model, trust, failure `resolution_state`, lifecycle, purge | T2, T5, T6, T8, T12, T13, T15 |
| 8 Canonical storage, transactions, activation journal, Markdown projection | T2, T3, T5, T7, T13, T15 |
| 9 Retrieval and ranking | T8, T10, T11, T12, T15 |
| 10 Pruning | T8, T12, T15 |
| 11 SQLite operations, activation journal, exact FTS tokenizer/SQLITE_FULL contract, FTS repair, backup reconciliation, and migrations | T2, T3, T9, T13, T14, T15 |
| 12 MCP and CLI contracts | T5, T10, T11, T12, T14, T15 |
| 13 Implementation structure | T1–T14 |
| 14 Acceptance gates | T15 plus the mapped gates above |
| 15 Rejected/deferred alternatives | T1, T15, ROADMAP.md |
| 16 Maturity receipt | T15 |

## Final self-review checklist

- [ ] Every included specification section and section-14 gate maps to a task and executable evidence; the gate-header table and each task header are machine-checked for exact bidirectional equality.
- [ ] Every task has exact files, interfaces, dependencies, tests, RED/GREEN commands, and a commit boundary; the advertised DAG is machine-checked against every task header and is acyclic.
- [ ] No task depends on undefined types, functions, migration names, or generated artifacts.
- [ ] Shared files have an explicit reconciler owner.
- [ ] TDD, no-secret-output, no-network, scoped mutation/auth contracts, optimistic revision, atomic pin reserves, FTS eligibility, projection durability, activation journal recovery, and lifecycle locks are tested before implementation claims.
- [ ] T2 owns replaceable `ManagedFileStore` and `ActivationJournal` ports; T7 has direct T4 scanner dependency and compiling fakes; T9 is the concrete filesystem/wiring boundary; T13 owns rule-upgrade activation and both backup-reconcile operator commands; T14 owns the final one-binary composition root and wiring tests.
- [ ] Every G00–G30 matrix row has a named owner, spec section, literal command, expected exit code, negative fixture, receipt path/hash, and tested source commit/tree; the completeness test rejects candidate-identity fields and fails when any required field is absent.
- [ ] Every stable requirement ID has exactly one owner task, named RED/GREEN test and negative fixture, requirement-table row, and gate row; `TestRequirementTraceability` checks task→test→gate and gate→test→task equality in both directions.
- [ ] Purge is receipt/lock-authorized and succeeds without scanner availability while emitting no content; restore is receipt/lock-authorized, scanner-gated, and fails closed without output or state change when scanning is unavailable.
- [ ] CP5 follows the exact C0→identity-free E→C1 protocol: all executable bytes are committed in C0 and tested there, only identity-free evidence is committed as E/C1, each G00–G30 literal command and suite command is rerun on clean C1 with external raw receipts and no subsequent bytes, and the external control-plane ledger alone binds C1, the external C1 matrix/receipt hashes, frozen status/manifest, and verdicts. The self-reference check rejects any committed artifact containing its own final candidate identity or a `candidate_commit`/`candidate_tree` matrix field.
- [ ] The plan contains no `TODO`, `TBD`, “implement later,” “appropriate error handling,” or similar placeholder.
- [ ] Post-v0.0.1 roadmap work is explicitly excluded.
