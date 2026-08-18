# Talaria-Mem Roadmap

This roadmap separates the target v0.0.1 journey from later capability and
hardening. It is not a release checklist. The current checkout is summarized
in [docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md); items that
are not composed or freshly evidenced remain open before a release.
The active consumer-install list is [docs/TASKS.md](docs/TASKS.md).

## v0.0.1 target — Explicit local memory

- One local Go binary and per-user daemon.
- Codex SessionStart and MCP integration.
- Explicit CLI, MCP, and import writes.
- SQLite-canonical memory with FTS5 retrieval.
- Transactionally scheduled deterministic Markdown projection.
- Betterleaks on every content boundary.
- Default-unverified writes with an explicit CLI `--verified` trust assertion.
- Workspace inference, explicit binding, and merge-with-redirect.
- Usage-aware ranking and conservative pruning recommendations.
- No transcript ingestion, model extraction, embeddings, or outbound network.

## After v0.0.1 — Optional local extraction

- Add an extractor port without changing canonical storage contracts.
- Disabled by default and enabled per workspace.
- Accept only literal loopback addresses or Unix sockets.
- Support local OpenAI-compatible servers such as Ollama or llama.cpp.
- Disable redirects and reject any non-local destination.
- Run Betterleaks scan, redaction, and rescan before local model invocation.
- Persist only sanitized bounded jobs; erase successful payloads.
- Mark extracted memories unverified until confirmed.
- Add PreCompact and SessionEnd only with this capability.

Cloud extraction is not scheduled. The local binary must not contain a dormant
cloud upload path.

## Retrieval evolution

- Evaluate FTS5 trigram indexing against real identifier and substring
  fixtures; adopt only when it materially improves retrieval.
- Add optional local embeddings only after lexical retrieval is measured.
- Keep embeddings rebuildable and non-canonical.
- Record model ID, dimensions, content hash, and generation version.
- Never require embeddings for ordinary operation.

## Distribution and platforms

- Add an NPX convenience wrapper around verified native binaries.
- Add Windows service lifecycle integration after the macOS and Linux journeys
  are stable.
- Expand packaging only with consumer installation evidence.

## v0.1.0 candidate hardening

- Declare supported persistence and API compatibility contracts.
- Exercise upgrades from every supported released schema.
- Add targeted fuzzing and property-based tests for parsers, imports, and
  projection recovery.
- Close reachable local authorization and resource-containment gaps.
- Reconsider capability-separated loopback tokens if the supported local
  threat model expands beyond one operating-system user.
- Evaluate OS-mediated user-presence authorization if same-user Codex command
  execution becomes untrusted; `--verified` alone is not such proof.
- Add crash-safe cryptographic root-key rotation with manifest
  reauthentication if operational evidence requires rotation.
- Establish reproducible release and supply-chain verification.
- Validate supported platform lifecycle and failure behavior.
- Publish a precise threat model and deferred-risk closure receipt.

## Future architecture

- Explore hosted multi-tenant storage as a separate deployment architecture
  with explicit tenant authentication and isolation.
- Do not infer synchronization or upload of existing local databases.
- Revisit scale, sharding, archival, and capacity policy only with measured
  workloads.
