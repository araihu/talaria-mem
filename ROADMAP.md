# Talaria-Mem Roadmap

This roadmap tracks work after the current unreleased v0.0.2 local journey. It
is not a release checklist. The current checkout is summarized in
[docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md); items that are
not composed or freshly evidenced remain open before a release.
The active consumer-install list is [docs/TASKS.md](docs/TASKS.md).

## Baseline — local memory with safe automatic curation

- One local Go binary and per-user daemon.
- Codex SessionStart and MCP integration.
- Explicit CLI, MCP, and import writes.
- SQLite-canonical memory with FTS5 retrieval.
- Transactionally scheduled deterministic Markdown projection.
- Betterleaks on every content boundary.
- Default-unverified writes with an explicit CLI `--verified` trust assertion.
- Generated trust for automatic/inline curation; generated context is
  searchable and recallable but cannot be pinned or projected until confirmed.
- Workspace inference, explicit binding, and merge-with-redirect.
- Usage-aware ranking and conservative pruning recommendations.
- Encrypted bounded curation jobs with restart recovery and expiry.
- Codex Luna-high app-server inference by default, ordered compatible-provider
  fallback, and local-only loopback configuration.
- Prompt-time recall and Codex UserPromptSubmit/PreCompact/SessionEnd hooks.
- Authenticated MCP stdio proxy and provider-free inline curation.

## Next — curation hardening and operator evidence

- Add a true Codex `thread/read` host snapshot adapter when the app-server
  compatibility surface is stable; retain the current bounded no-transcript
  degradation when host access is unavailable.
- Exercise live Codex schema drift checks and one explicitly approved Luna
  acceptance session without storing payloads.
- Add provider health history and explicit curation repair receipts without
  including credentials, prompts, locators, or response bodies.
- Add fuzz/property tests for hook JSON, provider structured output, and MCP
  JSON-RPC framing.

Cloud discovery is not scheduled. Remote compatible providers remain explicit
user configuration; the binary must not discover or upload to an unconfigured
service.

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
