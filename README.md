# Talaria-Mem

![Talaria-Mem — talária alada de Hermes](assets/talaria-mem-social-preview.png)

Talaria-Mem is a local-first, workspace-scoped memory service for Codex. The
current checkout is an unreleased v0.0.2 implementation: one Go binary, a
SQLite-canonical store, and a rebuildable Markdown projection. Runtime code has
no cloud or local-model extraction path; the daemon contract is no outbound
network connections. Process-level egress evidence remains a release gate.

Licensed under the [MIT License](LICENSE).

## Prerequisites

- Go 1.26 or newer.
- `curl` if the SessionStart hook will be used.
- macOS or Linux for the documented local journey. Other platforms are not
  verified yet.
- An existing owner-controlled `$HOME/.codex` directory without group/world
  write bits for automatic hook registration; use `--codex-hooks` for another
  JSON registry.
- Put `$HOME/.local/bin` on `PATH` if the binary is installed there.

## First local installation

For a complete copy-paste runbook, Codex hook/MCP registration, service
templates, and the documentation gaps found during a local deployment, see
[docs/LOCAL_DEPLOYMENT.md](docs/LOCAL_DEPLOYMENT.md). The active implementation
list is [docs/TASKS.md](docs/TASKS.md).

Build outside the source tree and inspect the machine-readable setup plan:

```sh
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/talaria-mem" ./cmd/talaria-mem
export PATH="$HOME/.local/bin:$PATH"
talaria-mem setup codex --dry-run --json
talaria-mem setup codex --apply --json
```

The default private root is `$HOME/.talaria-mem`, containing `state/`,
`config/`, and `backups/`, all owner-only. First-install setup creates only
those managed directories during a dry-run; it does not create credentials,
configuration, or the hook. Apply creates the root key, bearer token, managed
setup metadata, an executable SessionStart hook at
`$HOME/.talaria-mem/config/session-start.sh`, and one managed group in
`$HOME/.codex/hooks.json`. Re-running apply is idempotent and preserves other
Codex hooks. The `.codex` directory must already exist; use
`--codex-hooks /absolute/path/hooks.json` for another JSON registry. Removal
requires the recorded fingerprint and preserves unrelated files:

```sh
fingerprint='paste-the-64-character-fingerprint-from-dry-run-here'
talaria-mem setup codex --remove \
  --fingerprint "$fingerprint" \
  --apply --json
```

The hook registration is limited to the official JSON hook registry. Setup does
not start a daemon or install a LaunchAgent/systemd unit. Start the daemon
explicitly in a second terminal. Never print or commit `root.key` or `token`.

For isolated runs, either leave all three path variables unset to use the
default root, or set `TALARIA_STATE_DIR`, `TALARIA_CONFIG_DIR`, and
`TALARIA_BACKUP_DIR` together to absolute canonical managed directories.
Normal commands require those directories to already exist, be owner-owned,
mode `0700`, and not be symlinks. See the [local deployment
runbook](docs/LOCAL_DEPLOYMENT.md) and the [generated environment
contract](internal/lifecycle/environment.md).

## Daily use

Run the daemon in one terminal when using HTTP, MCP, or the hook:

```sh
talaria-mem daemon --foreground
```

Use the same binary in another terminal to create and inspect local memories:

```sh
talaria-mem workspace create --name my-repo
talaria-mem workspace list
talaria-mem workspace bind --key path:/absolute/path/to/repo --workspace my-repo

add_json=$(talaria-mem memory add --workspace my-repo --kind state --title "..." --content "..." --json)
memory_id=$(printf '%s' "$add_json" | sed -n 's/.*"MemoryID":"\([^"]*\)".*/\1/p')
revision_id=$(printf '%s' "$add_json" | sed -n 's/.*"RevisionID":"\([^"]*\)".*/\1/p')
confirm_json=$(talaria-mem memory confirm --memory "$memory_id" --expected-revision "$revision_id" --json)
# Confirmation creates a new revision; use its RevisionID for later writes.
revision_id=$(printf '%s' "$confirm_json" | sed -n 's/.*"RevisionID":"\([^"]*\)".*/\1/p')
talaria-mem memory search --workspace my-repo --query "..."
talaria-mem memory get "$memory_id" --workspace my-repo
talaria-mem memory forget --memory "$memory_id" --expected-revision "$revision_id"
talaria-mem status
talaria-mem doctor
```

The CLI currently composes the application and SQLite adapters in-process;
daemon mode is the authenticated loopback surface for HTTP, MCP, and hooks.
Writes are unverified by default. Only the explicit CLI trust workflow can
make a memory verified; MCP and import do not provide a verified override.
Betterleaks scans content at each declared input/output boundary. Findings or
scanner uncertainty fail closed and do not return memory content.

## SessionStart and local interfaces

The hook accepts the official Codex `SessionStart` event (`session_id`, `cwd`,
and `hook_event_name`), accepts an optional `transcript_path` without opening
it, and forwards only the bounded fields needed to the authenticated loopback
daemon. Its response includes Codex's
`hookSpecificOutput.additionalContext` shape. The daemon resolves the
workspace from the persisted binding and selects only active, verified,
non-quarantined memories. It never reads, stores, or logs a transcript.

MCP and control routes require a bearer token and literal loopback access.
`/healthz` is the unauthenticated liveness endpoint; `/readyz` and control
routes are authenticated.

## Available maintenance commands

The currently composed maintenance commands are:

```sh
talaria-mem db backup reconcile --dry-run
receipt='/absolute/path/to/receipt.json' # use the path printed by --dry-run
talaria-mem db backup reconcile --apply "$receipt"
talaria-mem doctor --repair=fts --dry-run
```

`memory forget` is an explicit expected-revision mutation, not a global-lock
maintenance receipt. Cobra exposes help for database restore and scanner-rule
upgrade, but those runtime operations still fail closed as unavailable; they
are tracked in [IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md).

## Verification

Run the repository gates:

```sh
make test
make test-race
make vet
make openapi-lint
make check
```

The application runtime has no egress path. A cold build/gate cache may still
download the pinned `sqlc` and Vacuum tools; once cached, the commands are
repeatable without that download. `make openapi-lint` uses the checked-in
`api/openapi/vacuum.yaml` configuration and writes its bundle under `.build/`.

The G00–G30 catalog and traceability files describe the intended acceptance
protocol. Existing receipt artifacts are historical evidence, not a release
claim for this untagged checkout. See [IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md)
before treating a capability as end-to-end available.

## Deliberate v0.0.2 boundaries

This implementation has no cloud extraction, local model extraction, transcript
ingestion, embedding index, automatic Markdown synchronization, multi-user
service, or automatic database eviction. Deferred work and the longer-term
local extraction direction are recorded in [ROADMAP.md](ROADMAP.md).
