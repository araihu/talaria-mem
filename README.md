# Talaria-Mem

Talaria-Mem is local-first, workspace-scoped memory for Codex. v0.0.1 is a
single Go binary and a per-user loopback daemon. SQLite is canonical; Markdown
is a rebuildable projection. The daemon has no cloud or local-model extraction
path and makes no outbound network connections.

## First local installation

Build outside the source tree and inspect the exact setup change before it is
applied:

```sh
go build -o "$HOME/.local/bin/talaria-mem" ./cmd/talaria-mem
talaria-mem setup codex --dry-run
talaria-mem setup codex --apply
```

The default private root is `$HOME/.talaria-mem`, containing `state/`,
`config/`, and `backups/`, all owner-only. A first-install dry-run does not
create credentials or configuration; it creates only the managed directories
needed to inspect the change. Apply creates the private managed directories,
then creates the local root key and bearer token before writing the SessionStart
integration. Re-running apply is idempotent;
removal requires the installation fingerprint and preserves unrelated Codex
configuration.

For acceptance composition only, `TALARIA_STATE_DIR`,
`TALARIA_CONFIG_DIR`, and `TALARIA_BACKUP_DIR` may point at absolute canonical
managed directories. Normal commands validate that those directories already
exist, are owner-owned, mode `0700`, and are not symlinks. First-install apply
may create a missing private leaf and one safe immediate parent; it never
performs an unrestricted recursive mkdir.

## Daily journey

```sh
# Run the local daemon in the foreground while developing.
talaria-mem daemon --foreground

# Create/select the durable workspace before adding scoped memories.
talaria-mem workspace create --name my-repo
talaria-mem workspace list
talaria-mem workspace bind --key path:/absolute/path/to/repo --workspace my-repo

# Add an unverified memory; review and explicitly confirm when appropriate.
talaria-mem memory add --workspace my-repo --kind state --title "..." --content "..."
talaria-mem memory list --workspace my-repo
talaria-mem memory confirm --memory <memory-id> --expected-revision <revision>

# Search, inspect, and use explicit lifecycle operations.
talaria-mem memory search --workspace my-repo --query "..."
talaria-mem memory get <memory-id> --workspace my-repo
talaria-mem memory forget --memory <memory-id> --expected-revision <revision>
talaria-mem status
talaria-mem doctor
```

Writes are unverified by default. Only the explicit CLI trust workflow can
make a memory verified; MCP and import do not provide a verified override.
Betterleaks scans content at every declared input/output boundary. Findings or
scanner uncertainty fail closed and do not return memory content.

SessionStart selects only active, verified, non-quarantined memories for the
resolved workspace. It never reads, stores, or logs a transcript. MCP and HTTP
are authenticated literal-loopback surfaces; `/healthz` is liveness and
`/readyz` is readiness.

## Recovery and maintenance

All destructive or recovery work is explicit, receipt-bound, and protected by
the global maintenance lock:

```sh
talaria-mem db backup reconcile --dry-run
talaria-mem db backup reconcile --apply <receipt>
talaria-mem db restore --backup <id> --dry-run
talaria-mem scanner rules upgrade --candidate <path> --dry-run
talaria-mem doctor --repair=fts --dry-run
```

Unknown or unauthenticated backup entries keep readiness false and are never
deleted during startup. Purge is scanner-independent and non-content;
restore remains scanner-gated. Receipts contain metadata and fingerprints,
not memory content or secret matches.

## Verification

The repository-owned gates are deterministic and offline at runtime:

```sh
make test
make test-race
make vet
make openapi-lint       # Vacuum must be installed locally
make check
```

`make openapi-lint` uses the checked-in `api/openapi/vacuum.yaml` rules and
writes its bundle under `.build/`; it does not download rules or contact a
service. `go generate ./...` and the generated-code checks must be clean before
accepting a candidate. Acceptance gate IDs G00-G30 and
requirement/section traceability live under `test/acceptance/`.

## Deliberate v0.0.1 boundaries

There is no cloud extraction, local model extraction, transcript ingestion,
embedding index, automatic Markdown synchronization, multi-user service, or
automatic database eviction in this release. Deferred work and the longer-term
local extraction direction are recorded in [ROADMAP.md](ROADMAP.md).
