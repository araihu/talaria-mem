# Talaria-Mem Automatic Memory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add safe automatic conversation curation and prompt-time recall, with Codex Luna-high as the default provider, ordered OpenAI-compatible fallbacks, durable encrypted jobs, inline MCP curation, and reversible Codex setup.

**Architecture:** Keep storage, curation policy, provider adapters, Codex host integration, and lifecycle setup as separate packages. Hooks synchronously produce only bounded local recall and encrypted queue writes; one background worker invokes the configured provider chain and passes validated candidates through the existing scanner and memory mutation boundary. SQLite remains canonical, generated memory is searchable but not projectable, and only the runtime composition root may connect these pieces.

**Tech Stack:** Go 1.26.0, SQLite/FTS5, sqlc 1.29.0, `github.com/caarlos0/env/v11`, `github.com/g4s8/envdoc`, `github.com/pelletier/go-toml/v2`, `github.com/atombender/go-jsonschema` 0.24.1, `github.com/sourcegraph/jsonrpc2` 0.2.1, official MCP Go SDK, oapi-codegen, Betterleaks, `tiktoken-go`.

**Spec:** [2026-08-19-talaria-mem-automatic-memory-design.md](../specs/2026-08-19-talaria-mem-automatic-memory-design.md)

## Global Constraints

- Work from `/Users/guilhermecastro/.codex/worktrees/talaria-mem-auto-memory` on `codex/talaria-mem-auto-memory`.
- Use test-driven development for every behavior: add one focused failing test, prove the expected failure, add the minimum production code, then prove the focused test passes.
- Keep the fixed `TALARIA_*` environment contract in `internal/lifecycle/environment.go`, parsed once with `env/v11` and documented with `envdoc`. Provider topology belongs only in owner-only `providers.toml`; dynamic credential variable names resolve from the same captured environment map.
- Never persist raw transcripts, prompts, provider responses, reasoning, tool inputs, commands, environment values, file contents, or complete tool output.
- Never route scanner refusal, invalid structured output, suspicious content, policy refusal, or persistence failure to another provider.
- Keep projection and SessionStart verified-only. Only prompt-time recall may include generated memory.
- Generated code is committed and must be reproducible. Do not hand-edit `*.gen.go` or `internal/adapters/sqlite/sqlc/*`.
- No push, merge, release, daemon installation, daemon start, host setup application, or paid live inference is part of this plan.

---

## Task 1: Introduce provider contracts, typed configuration, and fallback policy

**Files:**

- Create: `internal/curation/types.go`
- Create: `internal/curation/errors.go`
- Create: `internal/curation/router.go`
- Create: `internal/curation/router_test.go`
- Create: `internal/providers/config/config.go`
- Create: `internal/providers/config/config_test.go`
- Create: `internal/providers/config/url_policy.go`
- Create: `internal/providers/config/url_policy_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write failing router classification tests**

  Cover strict order, default Codex-only chain, disabled curation, empty/duplicate chain refusal, retryable fallback for unavailable/timeout/rate-limit/authentication, and terminal stop for scanner, invalid output, policy, suspicious content, persistence, and domain failures.

  Define the application port exactly once in `internal/curation/types.go`:

  ```go
  type Curator interface {
      Curate(context.Context, CurationRequest) (CurationResult, error)
  }

  type CurationRequest struct {
      WorkspaceID    string
      Reason         Reason
      Watermark      int64
      AllowedKinds   []domain.MemoryKind
      ThreadLocator  []byte
      Snapshot       []byte
  }
  ```

  Run: `go test ./internal/curation -run 'TestRouter|TestClassify'`

  Expected: fail because `Router`, provider error classes, and contracts do not exist.

- [ ] **Step 2: Implement the smallest ordered router**

  Add stable error classes `unavailable`, `timeout`, `rate_limit`, `authentication`, `scanner_refusal`, `invalid_output`, `policy_refusal`, `suspicious_content`, `persistence`, and `domain_validation`. Make retryable/fallback behavior explicit methods on the class; do not infer it from strings. Return provider and model provenance in `CurationResult`, never raw response metadata.

  Run: `go test ./internal/curation -run 'TestRouter|TestClassify'`

  Expected: pass.

- [ ] **Step 3: Write failing TOML and credential-boundary tests**

  Test the built-in default (`codex`, `gpt-5.6-luna`, `high`, `90s`), `enabled=false`, ordered named entries, absent credential, mutually exclusive `credential_env`/`credential_file`, owner-only absolute token files, and environment snapshot semantics. Use injected `map[string]string`; tests must never mutate the process environment.

  Run: `go test ./internal/providers/config`

  Expected: fail because provider configuration is not implemented.

- [ ] **Step 4: Implement `providers.toml` parsing and URL policy**

  Use these closed provider forms:

  ```go
  type Provider struct {
      Type            string        `toml:"type"`
      Model           string        `toml:"model"`
      ReasoningEffort string        `toml:"reasoning_effort"`
      Command         string        `toml:"command"`
      BaseURL         string        `toml:"base_url"`
      CredentialEnv   string        `toml:"credential_env"`
      CredentialFile  string        `toml:"credential_file"`
      Timeout         time.Duration `toml:"-"`
  }
  ```

  Decode duration through a private wire struct so malformed values fail. Permit `https` remotely and plain `http` only for literal `127.0.0.0/8` or `[::1]`; reject hostnames such as `localhost`, userinfo, query/fragment, non-`/v1` base paths, redirects, proxies, and ambient credentials. Read config/token files with symlink and `0600` ownership checks matching existing security stores.

  Run: `go test ./internal/providers/config`

  Expected: pass.

- [ ] **Step 5: Verify dependency and repository hygiene**

  Run: `go mod tidy && go test ./internal/curation ./internal/providers/config && git diff --check`

  Expected: pass; `go.mod` retains direct TOML/config dependencies and contains no SDK dependency that hides the provider boundary.

- [ ] **Step 6: Commit the slice**

  Run: `git add go.mod go.sum internal/curation internal/providers/config && git commit -m "feat: define curation provider policy"`

---

## Task 2: Generate the curated Codex app-server protocol client

**Files:**

- Create: `api/codex/schema/0.144.5/` (checked-in output from the pinned host command)
- Create: `api/codex/methods.json`
- Create: `api/codex/generate.go`
- Create: `cmd/codexrpcgen/main.go`
- Create: `cmd/codexrpcgen/main_test.go`
- Create: `internal/providers/codex/protocol/models.gen.go`
- Create: `internal/providers/codex/protocol/client.gen.go`
- Create: `internal/providers/codex/protocol/notifications.gen.go`
- Create: `internal/providers/codex/protocol/generate_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `Makefile`

- [ ] **Step 1: Capture and inventory the exact 0.144.5 schema**

  Prove the host generator version, then emit into a temporary directory before copying only reviewed schema inputs with `apply_patch` or the repository generator:

  ```sh
  codex --version
  schema_tmp=$(mktemp -d)
  codex app-server generate-json-schema --out "$schema_tmp"
  ```

  Expected version: `codex-cli 0.144.5`. The reviewed roots are `v1/InitializeParams`, `v1/InitializeResponse`, `v2/ModelList*`, `v2/ThreadRead*`, `v2/ThreadFork*`, `v2/ThreadStart*`, `v2/TurnStart*`, `v2/TurnStartedNotification`, `v2/ItemCompletedNotification`, `v2/TurnCompletedNotification`, `v2/ErrorNotification`, plus every transitive `$ref`.

- [ ] **Step 2: Write failing manifest and generator tests**

  `api/codex/methods.json` must map exact wire names to schema names:

  ```json
  {
    "initialize": ["v1/InitializeParams", "v1/InitializeResponse"],
    "model/list": ["v2/ModelListParams", "v2/ModelListResponse"],
    "thread/read": ["v2/ThreadReadParams", "v2/ThreadReadResponse"],
    "thread/fork": ["v2/ThreadForkParams", "v2/ThreadForkResponse"],
    "thread/start": ["v2/ThreadStartParams", "v2/ThreadStartResponse"],
    "turn/start": ["v2/TurnStartParams", "v2/TurnStartResponse"]
  }
  ```

  Test duplicate method/type rejection, missing transitive references, deterministic ordering, JSONL-safe wrapper names, and notification discriminator generation.

  Run: `go test ./cmd/codexrpcgen ./internal/providers/codex/protocol`

  Expected: fail because the generator and generated files do not exist.

- [ ] **Step 3: Implement the repository-owned generator**

  Add `github.com/atombender/go-jsonschema v0.24.1` as a Go tool and `github.com/sourcegraph/jsonrpc2 v0.2.1` as a direct runtime dependency. The generator must walk `$ref` edges from the manifest roots, reject references escaping the versioned schema directory, emit a normalized curated schema, invoke `go-jsonschema` for models, and render typed `jsonrpc2.Conn.Call` wrappers plus notification constants. No production code may decode the giant untyped union envelopes.

  `api/codex/generate.go` must provide one reproducible command:

  ```go
  //go:generate go run ../../cmd/codexrpcgen -schema ./schema/0.144.5 -manifest ./methods.json -out ../../internal/providers/codex/protocol
  ```

  Run: `go generate ./api/codex && go test ./cmd/codexrpcgen ./internal/providers/codex/protocol`

  Expected: pass.

- [ ] **Step 4: Add the clean-generation gate**

  Extend `Makefile` so `make generate` includes `go generate ./api/codex` and `make check` fails when regeneration changes committed output.

  Run: `make generate && git diff --exit-code -- internal/providers/codex/protocol api/codex`

  Expected: no diff after the first intentional generated-file addition is staged.

- [ ] **Step 5: Commit the slice**

  Run: `git add Makefile go.mod go.sum api/codex cmd/codexrpcgen internal/providers/codex/protocol && git commit -m "feat: generate codex app server client"`

---

## Task 3: Implement and supervise the Codex Luna-high curator

**Files:**

- Create: `internal/providers/codex/process.go`
- Create: `internal/providers/codex/process_test.go`
- Create: `internal/providers/codex/client.go`
- Create: `internal/providers/codex/client_test.go`
- Create: `internal/providers/codex/snapshot.go`
- Create: `internal/providers/codex/snapshot_test.go`
- Create: `internal/providers/codex/curator.go`
- Create: `internal/providers/codex/curator_test.go`
- Create: `internal/providers/codex/testdata/fake_app_server.go`

- [ ] **Step 1: Write failing process and handshake tests**

  Drive a helper process over JSONL stdio and test `initialize`, `model/list`, process reuse, EOF/crash, malformed frame, timeout/cancellation, stderr bounding, and missing `gpt-5.6-luna`. Assert that errors expose only class/provider/opaque operation, not wire bodies.

  Run: `go test ./internal/providers/codex -run 'TestProcess|TestHandshake'`

  Expected: fail because the process supervisor does not exist.

- [ ] **Step 2: Implement the JSONL supervisor**

  Spawn the configured executable as `codex app-server --stdio` with a minimal explicit environment snapshot. Use `jsonrpc2.NewBufferedStream` with the plain object codec, one reusable connection, bounded stderr, and shutdown that first cancels in-flight work and then kills a stuck child after a short grace period. Map executable absence, EOF, and crash to `unavailable`; context deadlines to `timeout`.

  Run: `go test ./internal/providers/codex -run 'TestProcess|TestHandshake'`

  Expected: pass.

- [ ] **Step 3: Write failing source-snapshot and curation-flow security tests**

  Test bounded `thread/read` source capture independently from provider selection, then ephemeral `thread/fork` with `ephemeral=true` and a private empty cwd, `turn/start` using model `gpt-5.6-luna` and reasoning `high`, completed agent-message collection, strict structured output, completion/failure notification handling, cleanup, and interruption on approval/tool requests. Include overload and out-of-order notification cases.

  Run: `go test ./internal/providers/codex -run 'TestCurator'`

  Expected: fail because `Curator` is not implemented.

- [ ] **Step 4: Implement the Codex host snapshot adapter and narrow curator**

  Implement `SnapshotSource` over `thread/read` without opening `transcript_path`; it must emit the minimized host-neutral snapshot used by Task 7 even when Codex is not in the provider chain. Configure curation forks with `ephemeral=true`, read-only sandbox, approval policy `never`, tools disabled in the system instruction, and a private temporary cwd created with `0700`. Validate at most five candidates locally. Treat any server request for command execution, file change, dynamic tool, or approval as terminal suspicious content: interrupt the turn, discard output, and never fall back.

  Run: `go test ./internal/providers/codex -run 'TestCurator'`

  Expected: pass.

- [ ] **Step 5: Run focused race and cleanup checks**

  Run: `go test -race ./internal/providers/codex && git diff --check`

  Expected: pass with no leaked child process or temporary directory.

- [ ] **Step 6: Commit the slice**

  Run: `git add internal/providers/codex && git commit -m "feat: add codex luna curation provider"`

---

## Task 4: Implement the OpenAI-compatible curator and outbound privacy boundary

**Files:**

- Create: `internal/providers/openai/client.go`
- Create: `internal/providers/openai/client_test.go`
- Create: `internal/providers/openai/curator.go`
- Create: `internal/providers/openai/curator_test.go`
- Create: `internal/providers/openai/snapshot.go`
- Create: `internal/providers/openai/snapshot_test.go`

- [ ] **Step 1: Write failing snapshot-minimization tests**

  Feed transcripts containing tool inputs, shell commands, environment values, file contents, long outputs, secrets, invalid UTF-8, and canary `transcript_path` data. Assert output contains only bounded user/assistant text, tool names/status, and sanitized error summaries; cap at 128 KiB before encryption.

  Run: `go test ./internal/providers/openai -run 'TestSnapshot'`

  Expected: fail because the sanitizer does not exist.

- [ ] **Step 2: Implement scan, redact, and rescan**

  Reuse the Betterleaks boundary through an injected scanner interface. A remaining match, scan timeout, or scanner error must return `scanner_refusal`, erase temporary buffers, and prohibit fallback. Keep raw host events out of the snapshot type so accidental JSON marshaling cannot persist them.

  Run: `go test ./internal/providers/openai -run 'TestSnapshot'`

  Expected: pass.

- [ ] **Step 3: Write failing HTTP contract tests**

  Use `httptest.Server` on literal loopback. Verify `/v1/chat/completions`, strict response format request, bearer auth only when configured, no proxy use, no redirects, timeout/auth/rate-limit classification, malformed JSON, extra text, extra keys, provider policy refusal, and response-size limits.

  Run: `go test ./internal/providers/openai -run 'TestClient|TestCurator'`

  Expected: fail because the client does not exist.

- [ ] **Step 4: Implement the compatible provider**

  Build a dedicated `http.Client` with `Proxy: nil`, `CheckRedirect` always returning `http.ErrUseLastResponse`, explicit dialer timeout, request deadline, and per-request destination revalidation. Decode one strict object, reject more than five candidates and forbidden trust/scope/control fields, then zero the response buffer after validation.

  Run: `go test ./internal/providers/openai`

  Expected: pass.

- [ ] **Step 5: Commit the slice**

  Run: `git add internal/providers/openai && git commit -m "feat: add compatible curation provider"`

---

## Task 5: Add generated trust, searchable indexing, and downgrade-safe schema v4

**Files:**

- Create: `db/migrations/000004_automatic_curation.up.sql`
- Create: `db/migrations/000004_automatic_curation.down.sql`
- Create: `db/queries/curation.sql`
- Modify: `db/queries/memory.sql`
- Modify: `db/queries/projection.sql`
- Modify: `internal/domain/memory.go`
- Modify: `internal/domain/memory_test.go`
- Modify: `internal/application/trust.go`
- Modify: `internal/application/memory_service.go`
- Modify: `internal/application/memory_service_test.go`
- Modify: `internal/adapters/sqlite/repository.go`
- Modify: `internal/adapters/sqlite/repository_contract_test.go`
- Modify: `internal/adapters/sqlite/db_test.go`
- Modify: `internal/adapters/sqlite/diagnostics.go`
- Regenerate: `internal/adapters/sqlite/sqlc/*`

- [ ] **Step 1: Write failing domain tests for `generated` trust**

  Assert generated memory permits only `state`, `procedure`, and `failure`; cannot be pinned, global, or a standing instruction; can be confirmed to a new verified revision; and cannot overwrite an existing verified revision through generated creation.

  Run: `go test ./internal/domain ./internal/application -run 'Generated|Confirm'`

  Expected: fail because `TrustGenerated` and the generated mutation path do not exist.

- [ ] **Step 2: Add a server-assigned generated mutation path**

  Add `domain.TrustGenerated`, `MemoryService.CreateGenerated(ctx, GeneratedMutationRequest)`, and an atomic `CreateGeneratedBatch(ctx, []GeneratedMutationRequest)` used by the worker. Do not accept trust, pin, user-global, standing-instruction, or provenance actor from the caller. The service assigns generated trust and a closed provenance source (`automatic` or `inline`) after scanning.

  Run: `go test ./internal/domain ./internal/application -run 'Generated|Confirm'`

  Expected: pass.

- [ ] **Step 3: Write failing migration and FTS/projection tests**

  Test upgrade from v3, generated trust constraints on both tables, queue/counter table constraints, generated FTS inclusion, unverified FTS exclusion, verified-only projection and SessionStart, and down-migration refusal when generated memories or pending/running jobs exist.

  Run: `go test ./internal/adapters/sqlite -run 'Migration004|Generated|FTS|Projection|SessionStart'`

  Expected: fail because migration 4 and query updates do not exist.

- [ ] **Step 4: Implement migration 4 transactionally**

  Rebuild strict tables as required by SQLite to widen trust checks. Add a nullable generated fingerprint to `memories` plus a partial unique workspace/fingerprint index for active generated rows. Add `curation_jobs` with encrypted locator/payload blobs, reason priority, attempt count, safe error class, state, next attempt, expiry, and unique `(session_digest, watermark)` coalescing; add `curation_session_counters` with digest/count/watermark/expiry. Add indexes for claim time and expiry. The down migration must call a guard that aborts on generated rows or active jobs; it must never coerce or delete them.

  Update FTS insert/rebuild/diagnostic SQL to `trust IN ('verified', 'generated')`. Leave `db/queries/projection.sql` verified-only.

  Run: `make sqlc-generate && go test ./internal/adapters/sqlite -run 'Migration004|Generated|FTS|Projection|SessionStart'`

  Expected: pass.

- [ ] **Step 5: Verify all trust consumers**

  Run: `rg -n "trust = 'verified'|TrustVerified|TrustUnverified" internal db --glob '!internal/adapters/sqlite/sqlc/*'`

  Expected: every remaining verified-only filter is intentional and covered by either projection, SessionStart, or privilege tests.

- [ ] **Step 6: Commit the slice**

  Run: `git add db internal/domain internal/application internal/adapters/sqlite && git commit -m "feat: add generated memory trust and schema"`

---

## Task 6: Build encrypted durable job and session-counter storage

**Files:**

- Create: `internal/curation/job.go`
- Create: `internal/curation/store.go`
- Create: `internal/security/curation_cipher.go`
- Create: `internal/security/curation_cipher_test.go`
- Create: `internal/adapters/sqlite/curation.go`
- Create: `internal/adapters/sqlite/curation_test.go`
- Modify: `internal/ports/keys.go`

- [ ] **Step 1: Write failing purpose-separated encryption tests**

  Add independent HKDF purposes for session digest, locator AEAD, and payload AEAD. Test random nonces, authenticated job metadata as associated data, wrong-purpose/wrong-job refusal, tamper detection, bounded plaintext, and explicit plaintext buffer clearing.

  Run: `go test ./internal/security -run 'TestCuration'`

  Expected: fail because curation key purposes and cipher do not exist.

- [ ] **Step 2: Implement the curation cipher**

  Use the existing root-key deriver and AES-GCM pattern. Never expose keys or plaintext through errors. Define `SealLocator`, `OpenLocator`, `SealSnapshot`, and `OpenSnapshot` rather than a generic public encrypt function so purpose separation cannot be bypassed.

  Run: `go test ./internal/security -run 'TestCuration'`

  Expected: pass.

- [ ] **Step 3: Write failing repository contract tests**

  Cover atomic enqueue/coalesce, reason priority (`pre_compact` above `session_end` above `periodic`), keyed digest uniqueness, claim ordering, one running job, retry scheduling, terminal erase, successful erase, 24-hour purge, prompt-count increment, SessionEnd counter removal, and restart recovery. Inspect SQLite directly to prove no plaintext canary is stored.

  The port must remain storage-oriented:

  ```go
  type JobStore interface {
      Enqueue(context.Context, Job) (Job, bool, error)
      ClaimNext(context.Context, time.Time) (Job, bool, error)
      Retry(context.Context, string, int, time.Time, ErrorClass) error
      Finish(context.Context, string, JobState, ErrorClass) error
      PurgeExpired(context.Context, time.Time) (int64, error)
      IncrementPrompt(context.Context, SessionCounter) (SessionCounter, error)
      EndSession(context.Context, []byte) error
  }
  ```

  Run: `go test ./internal/adapters/sqlite -run 'TestCurationStore'`

  Expected: fail because the repository adapter does not exist.

- [ ] **Step 4: Implement the sqlc-backed store**

  Keep encryption outside SQLite queries. Coalescing updates only priority, combined reason flags, expiry, and the newest encrypted bounded snapshot for the same digest/watermark. Safe diagnostics contain only opaque job ID, provider name, class, attempt, and timestamps.

  Run: `make sqlc-generate && go test ./internal/adapters/sqlite -run 'TestCurationStore'`

  Expected: pass.

- [ ] **Step 5: Commit the slice**

  Run: `git add db/queries/curation.sql internal/adapters/sqlite internal/curation internal/security internal/ports && git commit -m "feat: persist encrypted curation jobs"`

---

## Task 7: Implement capture, validation, deduplication, and the single worker

**Files:**

- Create: `internal/curation/capture.go`
- Create: `internal/curation/capture_test.go`
- Create: `internal/curation/validator.go`
- Create: `internal/curation/validator_test.go`
- Create: `internal/curation/service.go`
- Create: `internal/curation/service_test.go`
- Create: `internal/curation/worker.go`
- Create: `internal/curation/worker_test.go`
- Modify: `internal/application/memory_service.go`
- Modify: `internal/adapters/sqlite/repository.go`
- Modify: `db/queries/memory.sql`

- [ ] **Step 1: Write failing candidate validator tests**

  Test five-candidate maximum, allowed kinds, title/content/tag limits, no trust/pin/global/control fields, no extra top-level keys, no text around JSON, no Talaria fence closure, no encoded tool/approval request, and normal imperative procedure text acceptance.

  Run: `go test ./internal/curation -run 'TestValidate'`

  Expected: fail because validation is not implemented.

- [ ] **Step 2: Implement strict decoding and normalized fingerprints**

  Decode with duplicate-key rejection, unknown-field rejection, and exactly one JSON value. Build exact normalized duplicate fingerprints from kind/title/content/tags/resolution state using the existing content normalization rules and a keyed digest. Use migration 4's partial unique fingerprint index plus a repository lookup so concurrent duplicates become idempotent no-ops.

  Run: `go test ./internal/curation ./internal/application -run 'Validate|Duplicate'`

  Expected: pass.

- [ ] **Step 3: Write failing enqueue-service tests**

  Test hook reason/watermark validation, HMAC session digest, capture-before-return, 32 KiB current-prompt cap, 128 KiB snapshot cap, encrypted enqueue, scanner degradation to no job, and no retention of raw session ID. Include `transcript_path` canaries that panic if opened.

  Run: `go test ./internal/curation -run 'TestEnqueue|TestCapture'`

  Expected: fail because the enqueue service does not exist.

- [ ] **Step 4: Implement bounded capture and enqueue**

  Define a host port `SnapshotSource.Capture(context.Context, CaptureRequest) (SanitizedSnapshot, error)`. The service appends the current prompt only after scanning/redaction/rescan, encrypts locator and snapshot with job-bound associated data, and returns before any provider inference.

  Run: `go test ./internal/curation -run 'TestEnqueue|TestCapture'`

  Expected: pass.

- [ ] **Step 5: Write failing worker tests**

  Use fake time and deterministic jitter. Cover restart claim, one provider call at a time, retry backoff, fallback contract, chain exhaustion, decrypt failure, expiry, scanner refusal, atomic generated creation, persistence failure, success/terminal payload erase, and safe status aggregation.

  Run: `go test ./internal/curation -run 'TestWorker'`

  Expected: fail because the worker does not exist.

- [ ] **Step 6: Implement the worker loop**

  The worker must claim one job, decrypt only for the attempt, call the router, validate mechanically, scan, dedupe, and invoke `CreateGeneratedBatch` once per accepted result set. Use bounded exponential retry with jitter and a maximum attempt count. `Close` cancels and waits; it never abandons a SQLite job in `running` state.

  Run: `go test -race ./internal/curation`

  Expected: pass.

- [ ] **Step 7: Commit the slice**

  Run: `git add internal/curation internal/application internal/adapters/sqlite db/queries/memory.sql && git commit -m "feat: process automatic memory jobs"`

---

## Task 8: Add prompt-time recall and versioned hook endpoints

**Files:**

- Modify: `api/openapi/talaria.yaml`
- Regenerate: `internal/adapters/http/openapi.gen.go`
- Modify: `internal/adapters/http/server.go`
- Modify: `internal/adapters/http/server_test.go`
- Create: `internal/adapters/codex/hook_event.go`
- Create: `internal/adapters/codex/hook_event_test.go`
- Create: `internal/adapters/codex/recall.go`
- Create: `internal/adapters/codex/recall_test.go`
- Create: `internal/adapters/codex/curation_hooks.go`
- Create: `internal/adapters/codex/curation_hooks_test.go`
- Modify: `internal/adapters/codex/request.go`
- Modify: `internal/adapters/sqlite/retrieval.go`
- Modify: `internal/retrieval/ranking.go`
- Modify: `internal/retrieval/search.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write failing event-parser tests**

  Test the four exact event names, duplicate-key rejection, body limits, valid UTF-8 normalization, retention only of `session_id`/`cwd`/event name plus bounded current prompt, and complete disregard for `transcript_path` and unknown fields.

  Run: `go test ./internal/adapters/codex -run 'TestHookEvent'`

  Expected: fail because generalized hook parsing does not exist.

- [ ] **Step 2: Implement typed hook parsing and cadence**

  Preserve current SessionStart behavior. `UserPromptSubmit` always attempts recall and increments the expiring counter; only counts divisible by ten enqueue periodic work. `PreCompact` and `SessionEnd` enqueue every time, with SessionEnd deleting the counter after enqueue attempt. Any local failure returns a valid empty hook response.

  Run: `go test ./internal/adapters/codex -run 'TestHookEvent|TestCadence'`

  Expected: pass.

- [ ] **Step 3: Write failing recall ranking and budget tests**

  Assert at most three results, first 1,024 UTF-8 query bytes, approximately 800 `cl100k_base` tokens, hard 8 KiB output cap, verified-before-generated tie break, generated score penalty, unverified exclusion, verified pinned standing-instruction labeling, generated/unconfirmed labeling, fenced context, and final content-output guard.

  Run: `go test ./internal/retrieval ./internal/adapters/codex -run 'Recall|GeneratedRanking|Budget'`

  Expected: fail because prompt recall and trust-aware ranking do not exist.

- [ ] **Step 4: Implement bounded prompt recall**

  Promote `github.com/pkoukk/tiktoken-go` to a direct dependency. Keep FTS BM25 relevance primary while applying explicit trust/pin/kind tie-breaks. Render only IDs, revision IDs, kind, trust, and guarded text under a historical-reference fence that cannot be closed by memory content.

  Run: `go test ./internal/retrieval ./internal/adapters/codex -run 'Recall|GeneratedRanking|Budget'`

  Expected: pass.

- [ ] **Step 5: Add authenticated versioned control endpoints**

  Preserve the existing `POST /control/v1/session-start` contract and add `/control/v1/user-prompt-submit`, `/control/v1/pre-compact`, and `/control/v1/session-end`. Reuse existing bearer/Host/Origin/content-type/body/deadline middleware. UserPromptSubmit returns optional recall context; enqueue-only endpoints return a bounded accepted response without job content.

  Run: `go generate ./api/openapi && go test ./internal/adapters/http -run 'Hook|Auth|BodyLimit|Duplicate'`

  Expected: pass and generated OpenAPI is current.

- [ ] **Step 6: Commit the slice**

  Run: `git add api/openapi internal/adapters/http internal/adapters/codex internal/adapters/sqlite/retrieval.go internal/retrieval go.mod go.sum && git commit -m "feat: recall memory from codex hooks"`

---

## Task 9: Add inline MCP curation and the authenticated stdio proxy

**Files:**

- Modify: `internal/adapters/mcp/server.go`
- Modify: `internal/adapters/mcp/tool_mutations.go`
- Modify: `internal/adapters/mcp/tool_mutations_test.go`
- Create: `internal/adapters/mcp/proxy.go`
- Create: `internal/adapters/mcp/proxy_test.go`
- Modify: `internal/cli/commands/commands.go`
- Create: `internal/cli/commands/mcp.go`
- Create: `internal/cli/commands/mcp_test.go`
- Modify: `internal/runtime/composition.go`

- [ ] **Step 1: Write failing `memory_curate_inline` tests**

  Test one candidate, required workspace scope, server-assigned generated trust/inline provenance, no provider invocation, and rejection of standing instruction, pin, verification, global scope, unsafe content, unknown fields, and duplicate JSON keys. Assert existing `memory_create` remains unverified.

  Run: `go test ./internal/adapters/mcp -run 'Inline|CreateRemainsUnverified'`

  Expected: fail because the inline tool does not exist.

- [ ] **Step 2: Implement the inline MCP tool**

  Give the handler a narrow `CreateGenerated` interface rather than the router or provider. The input schema must not contain trust, actor, pin, or global fields. Return the same safe mutation receipt shape as existing tools.

  Run: `go test ./internal/adapters/mcp -run 'Inline|CreateRemainsUnverified'`

  Expected: pass.

- [ ] **Step 3: Write failing stdio-proxy tests**

  Test initialize/tool request/notification bridging, JSON-RPC ID preservation, `Mcp-Session-Id` retention, owner-only token-file reading, bounded stdin frames, daemon unavailable message, non-2xx response handling, cancellation, and proof that token/body never reach stderr.

  Run: `go test ./internal/adapters/mcp ./internal/cli/commands -run 'Proxy|MCP'`

  Expected: fail because `talaria-mem mcp proxy` does not exist.

- [ ] **Step 4: Implement the proxy and command**

  Bridge newline-delimited stdio JSON-RPC to the existing loopback Streamable HTTP `/mcp` endpoint. Maintain the session header in memory, use no proxy or redirects, load the bearer token from the configured owner-only file, and emit one actionable content-free error when the daemon is absent.

  Run: `go test ./internal/adapters/mcp ./internal/cli/commands -run 'Proxy|MCP'`

  Expected: pass.

- [ ] **Step 5: Commit the slice**

  Run: `git add internal/adapters/mcp internal/cli/commands internal/runtime/composition.go && git commit -m "feat: add inline curation mcp proxy"`

---

## Task 10: Expand reversible Codex setup to all hooks, MCP, and provider defaults

**Files:**

- Modify: `internal/lifecycle/setup.go`
- Modify: `internal/lifecycle/setup_test.go`
- Modify: `internal/lifecycle/codex_hooks.go`
- Modify: `internal/lifecycle/codex_hooks_test.go`
- Modify: `internal/lifecycle/codex_installer.go`
- Modify: `internal/lifecycle/codex_installer_test.go`
- Create: `internal/lifecycle/codex_mcp.go`
- Create: `internal/lifecycle/codex_mcp_test.go`
- Create: `internal/lifecycle/provider_config.go`
- Create: `internal/lifecycle/provider_config_test.go`
- Modify: `internal/runtime/composition.go`

- [ ] **Step 1: Write failing four-hook ownership tests**

  Cover SessionStart matcher `startup|resume|clear|compact`, UserPromptSubmit with no matcher, PreCompact, and SessionEnd. Test dry-run/apply/remove, idempotence, unrelated hook preservation, command collision, duplicate managed group, symlink refusal, concurrent edit collision, and rollback after a later artifact fails.

  Run: `go test ./internal/lifecycle -run 'CodexHooks|Setup'`

  Expected: fail because setup owns only SessionStart.

- [ ] **Step 2: Generalize the hook installer**

  Install one owner-only `codex-hook.sh` that parses the event name and POSTs to the matching versioned endpoint without exposing the token. Represent managed hook groups as a closed table, fingerprint every exact event/path tuple, and remove only the matching Talaria groups.

  Run: `go test ./internal/lifecycle -run 'CodexHooks|Setup'`

  Expected: pass for hook cases.

- [ ] **Step 3: Write failing MCP TOML and provider-default tests**

  Test semantic preservation of unrelated `~/.codex/config.toml` entries, exact `mcp_servers.talaria_mem` command/args, collision refusal, no token in TOML/arguments, absent-only `providers.toml` creation at `0600`, existing provider-file preservation, dry-run receipts, remove ownership, and rollback ordering.

  Run: `go test ./internal/lifecycle -run 'CodexMCP|ProviderConfig|SetupRollback'`

  Expected: fail because these managed artifacts do not exist.

- [ ] **Step 4: Implement transactional multi-artifact setup**

  Expand `SetupRequest` with distinct `TalariaConfigPath`, `CodexConfigPath`, `HookPath`, `CodexHooksPath`, and `ProviderConfigPath`. Keep the current fingerprinted receipt model but plan/apply artifacts in this order: hook script, hooks JSON, MCP TOML entry, default provider config, Talaria metadata. On failure, restore successful writes only if their post-write fingerprints still match.

  Register MCP as the current binary plus arguments `mcp proxy`; let the proxy discover endpoint/token through Talaria-owned metadata, never Codex config.

  Run: `go test ./internal/lifecycle -run 'CodexMCP|ProviderConfig|Setup'`

  Expected: pass.

- [ ] **Step 5: Prove setup does not manage services**

  Run: `rg -n "launchctl|systemctl|daemon start|ListenAndServe" internal/lifecycle/codex_* internal/lifecycle/setup.go`

  Expected: no service start/install behavior in setup code.

- [ ] **Step 6: Commit the slice**

  Run: `git add internal/lifecycle internal/runtime/composition.go && git commit -m "feat: register codex hooks and mcp"`

---

## Task 11: Wire runtime lifecycle, status, doctor, and degradation reporting

**Files:**

- Modify: `internal/runtime/composition.go`
- Modify: `internal/runtime/composition_test.go`
- Modify: `internal/lifecycle/status.go`
- Modify: `internal/lifecycle/status_test.go`
- Modify: `internal/lifecycle/doctor.go`
- Modify: `internal/lifecycle/doctor_test.go`
- Modify: `internal/lifecycle/environment.go`
- Regenerate: `internal/lifecycle/environment.md`

- [ ] **Step 1: Write failing composition tests**

  Assert one-time environment capture, provider config load, credential resolution, explicit ordered provider construction, worker start after database readiness, worker stop before database close, disabled-provider behavior, Codex unavailable degradation, and no outbound client outside provider adapters.

  Run: `go test ./internal/runtime -run 'Provider|Worker|Close'`

  Expected: fail because the new graph is not wired.

- [ ] **Step 2: Wire the composition root**

  Add curation store/cipher/service/worker/router/provider fields to `Composition`. Construct only configured inference providers; when automatic curation is enabled, construct the separate Codex host snapshot adapter required for `thread/read` even if the inference chain is local-only. Missing Codex host access skips capture/enqueue and marks curation degraded but leaves HTTP/MCP/recall ready. Ensure constructor failure closes any started child/process/database in reverse order.

  Run: `go test -race ./internal/runtime -run 'Provider|Worker|Close'`

  Expected: pass.

- [ ] **Step 3: Write failing safe observability tests**

  Test configured provider order, per-provider availability, enabled state, queue depth, oldest safe age, running count, and last safe error class. Seed credentials, prompts, locator, candidate text, and provider body canaries and assert none occur in status/doctor JSON or errors.

  Run: `go test ./internal/lifecycle -run 'Provider|Curation|Canary'`

  Expected: fail because status and doctor do not expose safe curation health.

- [ ] **Step 4: Implement degraded curation health**

  Add a `CurationStatusSource` port returning metadata only. Provider degradation must not flip local `Ready` false. Queue corruption, decrypt failure, or scanner unavailability must close curation processing and produce a safe repair diagnostic without blocking read-only memory service.

  Run: `go test ./internal/lifecycle -run 'Provider|Curation|Canary'`

  Expected: pass.

- [ ] **Step 5: Revalidate the Go environment contract**

  Add no topology environment variables. If the proxy needs a fixed path override, add it to the existing `env/v11` struct and regenerate with `envdoc`; otherwise generation must stay byte-identical.

  Run: `make generate && git diff --check && go test ./internal/runtime ./internal/lifecycle`

  Expected: pass; `internal/lifecycle/environment.md` matches the typed fixed environment.

- [ ] **Step 6: Commit the slice**

  Run: `git add internal/runtime internal/lifecycle && git commit -m "feat: wire automatic memory runtime"`

---

## Task 12: Add end-to-end acceptance, documentation, and final gates

**Files:**

- Create: `internal/acceptance/automatic_memory_test.go`
- Create: `scripts/check-codex-schema.sh`
- Create: `scripts/live-codex-curation.sh`
- Modify: `README.md`
- Modify: `ROADMAP.md`
- Modify: `docs/status.md`
- Modify: `docs/deployment/local.md`
- Modify: `Makefile`

- [ ] **Step 1: Write a failing offline acceptance test**

  Exercise setup in a temporary home, daemon graph with fake Codex and compatible providers, UserPromptSubmit recall, tenth-prompt enqueue, PreCompact priority, SessionEnd cleanup, worker restart, generated memory search, verified-only projection/SessionStart, inline MCP curation, safe status, and setup removal. Include secret/transcript/tool canaries and assert none persist in SQLite or emitted output.

  Run: `go test ./internal/acceptance -run TestAutomaticMemoryLifecycle`

  Expected: fail until every cross-package seam is connected.

- [ ] **Step 2: Make the acceptance path pass without live inference**

  Add only test seams required to inject process/HTTP fakes and time. Do not add alternate production code paths, global provider registries, or package-init hooks.

  Run: `go test -race ./internal/acceptance -run TestAutomaticMemoryLifecycle`

  Expected: pass.

- [ ] **Step 3: Add opt-in Codex compatibility scripts**

  `scripts/check-codex-schema.sh` must require `codex-cli 0.144.5`, generate to `mktemp -d`, run the repository curator/pruner, and diff only schema/protocol metadata. `scripts/live-codex-curation.sh` must require `TALARIA_LIVE_CODEX=1`, use a synthetic non-secret fixture, record only CLI version/provider/model/result count, and never run under `make check`.

  Run: `scripts/check-codex-schema.sh`

  Expected on this host: pass against Codex 0.144.5 without modifying the worktree.

- [ ] **Step 4: Document configuration and operator behavior**

  Document the Codex Luna-high default, disabled mode, local Ollama example at `http://127.0.0.1:11434/v1`, local-only chain, explicit Codex fallback, remote HTTPS credentials, trust labels, queue retention, hook/MCP setup receipts, degraded status, privacy exclusions, downgrade guard, and the fact that setup does not start/install the daemon.

  Run: `rg -n "gpt-5.6-luna|reasoning_effort|enabled = false|127.0.0.1:11434|memory_curate_inline|24 hours" README.md docs ROADMAP.md`

  Expected: every operational contract is discoverable.

- [ ] **Step 5: Run focused security and generated-source review**

  Run:

  ```sh
  rg -n "transcript_path|credential|Authorization|Snapshot|ThreadLocator" internal --glob '*.go'
  rg -n "http\.Client|http\.Transport|exec\.Command" internal --glob '*.go'
  make generate
  git diff --exit-code -- internal/adapters/http/openapi.gen.go internal/adapters/sqlite/sqlc internal/providers/codex/protocol internal/lifecycle/environment.md
  ```

  Expected: network/process ownership exists only in approved adapters; no generated drift.

- [ ] **Step 6: Run the complete repository gate**

  Run:

  ```sh
  go test ./...
  go test -race ./...
  go vet ./...
  make check
  git diff --check
  git status --short
  ```

  Expected: all tests/gates pass; status shows only intentional committed plan/implementation history and no generated or temporary residue.

- [ ] **Step 7: Perform one scoped final review**

  Review only the branch diff against `main` for spec coverage, fallback safety, trust escalation, content retention, setup reversibility, lifecycle cleanup, and test evidence. Fix discovered defects once and rerun the smallest affected gate plus `make check`.

- [ ] **Step 8: Commit the final slice**

  Run: `git add Makefile README.md ROADMAP.md docs internal/acceptance scripts && git commit -m "test: verify automatic memory lifecycle"`

  Stop after reporting branch, commits, and verification evidence. Do not push, merge, release, install, apply host setup, start a service, or run the paid live-inference script without separate user authorization.
