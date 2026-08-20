# Local deployment runbook

This page is the operator path for a source checkout. Follow it in order:
build the binary, create the private installation, register the official Codex
lifecycle hooks and MCP proxy, start the daemon, and verify a workspace. Host service
installation remains a separate, explicit step because the binary does not
install or start service-manager units.

The path below was exercised on macOS 26 with Go 1.26.6, `curl`, and a candidate
based on `origin/main`. The isolated smoke used temporary `TALARIA_*_DIR`
directories and a non-default loopback port, so it did not alter the operator's
real memory database.

A successful run ends with three checks: `/healthz` returns HTTP 200 with
`"status":"ok"`, a confirmed memory appears in `memory search`, and a
SessionStart hook preflight returns `hookSpecificOutput` without returning the
submitted `transcript_path`. This runbook is an operator recipe for the current
checkout, not release evidence or a supported host-service installer.

## Prerequisites

- Go 1.26 or newer.
- macOS or Linux. Windows is not verified.
- `curl` for the Codex SessionStart hook.
- A Codex CLI version with hooks enabled. Check with `codex features list`.
- An existing owner-controlled `$HOME/.codex` directory without group/world
  write bits. The installer writes the official JSON registry only; it does
  not create the Codex directory tree.

The runtime is local-first: SQLite is canonical, the daemon binds to literal
loopback by default at `127.0.0.1:7437`, and automatic inference is enabled
only through the user-owned provider topology.

## Build and install the binary

Run from the repository root. Keep the source checkout and the installed binary
separate so a later checkout cleanup cannot remove the executable:

```sh
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/talaria-mem" ./cmd/talaria-mem
export PATH="$HOME/.local/bin:$PATH"
git rev-parse --verify HEAD
go version
talaria-mem --help
```

The repository currently distributes source, not a versioned native binary or
checksum. Pin the Git commit used for the build and retain it with the local
installation record.

## CLI contract

Use `talaria-mem --help` or any `talaria-mem <command> --help` for generated
usage; help is side-effect-free and does not require credentials, SQLite, or a
running daemon. Pass `--json` as a persistent flag before or after the command
when scripting. Unknown flags and invalid positional arguments fail with exit
code `2`; machine-readable command results go to stdout and human diagnostics
go to stderr.

`daemon --address HOST:PORT` is the one bootstrap option: it is consumed before
runtime composition so authentication and the listener share the same
authority. Setup must use the same endpoint, and the generated hook receives
that endpoint by default. Use the literal IPv4 loopback form in the documented
hook path:

| Purpose | Default | Override |
|---|---|---|
| Daemon listener | `127.0.0.1:7437` | `daemon --address 127.0.0.1:<port>` |
| Setup metadata, generated hook, and Codex registration | `127.0.0.1:7437` | `setup codex --endpoint 127.0.0.1:<port>` |
| One generated-hook invocation | setup endpoint | `TALARIA_ENDPOINT=http://127.0.0.1:<port>` |

The setup and daemon authorities must match. `TALARIA_ENDPOINT` changes only
the generated shell hook invocation; it does not reconfigure an already-running
daemon.

## Create the private installation

The default root is `$HOME/.talaria-mem`:

```sh
talaria-mem setup codex --dry-run --json
talaria-mem setup codex --apply --json
```

By default setup targets `$HOME/.codex/hooks.json`. Use
`--codex-hooks /absolute/path/hooks.json` when Codex uses another JSON hook
registry or when running an isolated fixture. The parent directory must exist,
be owned by the current user, and have no group/world write bits.

The dry-run JSON includes the setup fingerprint. Save that value if the
installation may later be removed; removal is intentionally receipt-based:

```sh
fingerprint='paste-the-64-character-fingerprint-from-dry-run-here'
talaria-mem setup codex --remove \
  --fingerprint "$fingerprint" \
  --apply --json
```

If a non-default endpoint or custom paths were used, pass the same
`--endpoint`, `--config`, `--talaria-config`, `--codex-config`, `--hook`,
`--codex-hooks`, `--provider-config`, `--token`, and `--binary` values during
removal. A changed or unknown managed hook is treated as a collision rather
than overwritten.

For a non-default loopback port, pass the same authority to setup and daemon:

```sh
talaria-mem setup codex --apply --json --endpoint 127.0.0.1:8743
talaria-mem daemon --foreground --address 127.0.0.1:8743
```

The generated hook bakes the setup endpoint into its default. An explicit
`TALARIA_ENDPOINT=http://127.0.0.1:<port>` environment variable overrides it
for that hook invocation.

The dry-run creates only the owner-private directory structure. Apply creates
the root key, bearer token, setup metadata, generic executable hook, four
managed official Codex hook groups, MCP registration, and absent-only provider
configuration:

```text
$HOME/.talaria-mem/state/talaria-mem.sqlite3
$HOME/.talaria-mem/config/root.key
$HOME/.talaria-mem/config/token
$HOME/.talaria-mem/config/codex.toml
$HOME/.talaria-mem/config/codex-hook.sh
$HOME/.talaria-mem/config/providers.toml
$HOME/.codex/config.toml (managed MCP block)
$HOME/.codex/hooks.json
$HOME/.talaria-mem/backups/
```

Do not print or commit `root.key` or `token`. Re-running `--apply` is
idempotent. Setup does not start a daemon or install a service. It adds only
the exact Talaria-Mem hook groups and MCP block; unrelated configuration
remains in place.

The normal installation uses these defaults:

| Variable | Default | Contains |
|---|---|---|
| `TALARIA_STATE_DIR` | `$HOME/.talaria-mem/state` | SQLite database and lifecycle state |
| `TALARIA_CONFIG_DIR` | `$HOME/.talaria-mem/config` | root key, bearer token, and setup artifacts |
| `TALARIA_BACKUP_DIR` | `$HOME/.talaria-mem/backups` | authenticated backup files and sidecars |

If none of the three variables is set, the defaults are selected. If one is
set, set all three to absolute, canonical directories before setup; they are
one typed configuration boundary:

```sh
export TALARIA_STATE_DIR="$HOME/.talaria-mem/state"
export TALARIA_CONFIG_DIR="$HOME/.talaria-mem/config"
export TALARIA_BACKUP_DIR="$HOME/.talaria-mem/backups"
```

Do not set only one or two of these variables. Runtime validation requires the
complete set, owner-only directories, and non-symlink paths. Setup may create
the final directories on first install; normal daemon composition requires
them to already exist and validates them again. The generated contract is also
kept at [`internal/lifecycle/environment.md`](../internal/lifecycle/environment.md).

## Start and verify the daemon

Start it in a dedicated terminal:

```sh
talaria-mem daemon --foreground
```

Use `--address 127.0.0.1:<port>` when setup used a non-default endpoint. The
address is a literal loopback host and port, not a public bind address or a
hostname.

In another terminal:

```sh
curl --fail --noproxy '*' http://127.0.0.1:7437/healthz
talaria-mem status
talaria-mem doctor
```

Expected liveness output includes `"status":"ok"`. Stop the foreground
process with `Ctrl-C`. There is no portable `talaria-mem service install`
command yet.

### Optional host service templates

Templates exist under `packaging/macos/` and `packaging/linux/`, but they are
not filled, installed, or tested end-to-end by `setup`. They assume the default
`$HOME/.talaria-mem` paths; they do not declare custom `TALARIA_*_DIR` values.
Do not rely on shell exports for a service-manager process. Use a foreground
daemon for a custom environment until a service installer exists.

On macOS, after replacing `{{TALARIA_BINARY}}` and `{{TALARIA_ADDRESS}}`:

```sh
mkdir -p "$HOME/Library/LaunchAgents"
sed -e "s#{{TALARIA_BINARY}}#$HOME/.local/bin/talaria-mem#g" \
  -e "s#{{TALARIA_ADDRESS}}#127.0.0.1:7437#g" \
  packaging/macos/com.araihu.talaria-mem.plist \
  > "$HOME/Library/LaunchAgents/com.araihu.talaria-mem.plist"
launchctl bootstrap "gui/$(id -u)" \
  "$HOME/Library/LaunchAgents/com.araihu.talaria-mem.plist"
launchctl kickstart -k "gui/$(id -u)/com.araihu.talaria-mem"
launchctl print "gui/$(id -u)/com.araihu.talaria-mem"
```

On Linux with a user systemd instance:

```sh
mkdir -p "$HOME/.config/systemd/user"
sed -e "s#{{TALARIA_BINARY}}#$HOME/.local/bin/talaria-mem#g" \
  -e "s#{{TALARIA_ADDRESS}}#127.0.0.1:7437#g" \
  packaging/linux/talaria-mem.service \
  > "$HOME/.config/systemd/user/talaria-mem.service"
systemctl --user daemon-reload
systemctl --user enable --now talaria-mem.service
systemctl --user status --no-pager talaria-mem.service
journalctl --user -u talaria-mem.service -n 50 --no-pager
```

Treat these as operator recipes, not release-supported installers. Verify the
result with `talaria-mem doctor` and the loopback health endpoint. To roll back
one of these exact recipes, stop and unload the unit first, then remove only
the generated file:

```sh
# macOS
launchctl bootout "gui/$(id -u)/com.araihu.talaria-mem" || true
rm "$HOME/Library/LaunchAgents/com.araihu.talaria-mem.plist"

# Linux
systemctl --user disable --now talaria-mem.service || true
rm "$HOME/.config/systemd/user/talaria-mem.service"
systemctl --user daemon-reload
```

## Register Codex hooks and MCP

`setup codex --apply` writes
`$HOME/.talaria-mem/config/codex-hook.sh` and registers one exact managed group
for each of `SessionStart`, `UserPromptSubmit`, `PreCompact`, and `SessionEnd`
in `$HOME/.codex/hooks.json`. It also adds the exact `mcp_servers.talaria_mem`
block to `$HOME/.codex/config.toml` and creates the default `providers.toml`
only when absent. It preserves unrelated configuration. A second apply is a
no-op. `--codex-hooks` can target a different JSON registry, and
`--codex-config`/`--provider-config` can target explicit files.

The managed group is:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|resume|clear|compact",
        "hooks": [
          {
            "type": "command",
            "command": "/absolute/path/to/home/.talaria-mem/config/codex-hook.sh",
            "statusMessage": "Loading Talaria-Mem context"
          }
        ]
      }
    ]
  }
}
```

The [Codex hooks reference](https://developers.openai.com/codex/hooks/) defines
common input fields such as `session_id`, `cwd`, `hook_event_name`, and optional
`transcript_path`, plus the SessionStart `source` field. The generated hook
accepts that payload but retains only the three bounded fields needed by the
daemon; it discards `source`, `transcript_path`, and other unknown fields
without opening the transcript. Its JSON response uses the official
`hookSpecificOutput.additionalContext` shape. The matcher values (`startup`,
`resume`, `clear`, and `compact`) select when Codex runs the hook; the payload's
`hook_event_name` remains `SessionStart`.

The installer writes the actual absolute hook path. Keep the hook synchronous so
SessionStart waits for the bounded response. Start the daemon before opening
Codex sessions. In Codex, run `/hooks`, review the command, and trust the exact
hook definition; a changed hook is reviewed again. If the existing entry uses
the same command with different matcher or status fields, setup returns a
collision and leaves the file unchanged.

The generic hook routes UserPromptSubmit, PreCompact, and SessionEnd to the
versioned control endpoints. UserPromptSubmit returns bounded local recall and
queues every tenth prompt; the other events enqueue without waiting for model
inference. The intended privacy boundary is to forward only bounded event
fields and never read or persist `transcript_path`.

### Provider order and local-only inference

The absent-only `providers.toml` defaults to Codex `gpt-5.6-luna` with
`reasoning_effort = "high"` and a 90-second timeout. To use local inference
without Codex fallback, configure a single literal-loopback provider:

```toml
version = 1
enabled = true
chain = ["ollama"]

[providers.ollama]
type = "openai_compatible"
base_url = "http://127.0.0.1:11434/v1"
model = "qwen3:8b"
timeout = "90s"
```

Fallback is attempted only for provider-unavailable, timeout, rate-limit, or
authentication classes. Scanner refusal, invalid structured output, policy or
suspicious-content rejection, and persistence failures stop the chain.

### Privacy and redaction checks

The consumer installer does not read credential bytes into a receipt. Dry-run
JSON contains paths, actions, and a setup fingerprint only. Malformed or
duplicate `hooks.json` keys return stable errors without echoing the offending
value. Existing unknown JSON values are preserved in the file but never copied
into installer diagnostics. The official registry is created or replaced with
mode `0600`; the generated script remains mode `0700`; symlinks and writable
group/world parents fail closed.

Focused checks:

```sh
go test ./internal/lifecycle ./internal/adapters/http ./internal/adapters/codex ./internal/adapters/mcp
sh -n internal/lifecycle/assets/codex-hook.sh
```

These tests use credential and submitted-content canaries. A canary appearing
in a test failure is a privacy bug, not an expected diagnostic.

After creating and binding a workspace, the generated hook can be preflighted
without opening Codex:

```sh
hook="$HOME/.talaria-mem/config/codex-hook.sh"
printf '%s\n' '{"session_id":"manual-smoke","cwd":"/absolute/path/to/repo","hook_event_name":"SessionStart","transcript_path":"/tmp/should-not-be-read.jsonl"}' \
  | "$hook"
```

Replace the `cwd` with the exact path used by `workspace bind`. The response
should contain `hookSpecificOutput.additionalContext`; the transcript path
must not be returned. A `404` means that the path has no workspace binding,
not that the daemon is down.

## MCP proxy

Setup registers the `talaria_mem` MCP server as `talaria-mem mcp proxy`. The
proxy reads the owner-only token file and bridges Codex stdio JSON-RPC to the
authenticated loopback Streamable HTTP endpoint; no token environment variable
or bearer value is written to Codex configuration.

```sh
talaria-mem setup codex --dry-run --json
talaria-mem setup codex --apply --json
codex mcp list
```

Use the same custom port in setup and daemon when the daemon does not use
`7437`. `memory_curate_inline` performs no provider call; it stores one
server-assigned generated candidate after the existing scanner/domain guards.

The token is local bearer material. Rotate it with `talaria-mem token rotate`
only after updating the environment used by Codex, then restart the Codex
process that owns the MCP connection.

## Create a workspace and verify retrieval

Create a workspace before adding memories. Use an absolute path binding for
the repository that Codex will open:

```sh
talaria-mem workspace create --name my-repo
talaria-mem workspace bind \
  --key "path:$(pwd)" \
  --workspace my-repo
add_json="$(talaria-mem memory add \
  --workspace my-repo \
  --kind state \
  --title "Local deployment smoke" \
  --content "Talaria-Mem is running locally" \
  --json)"
memory_id="$(printf '%s' "$add_json" | sed -n 's/.*"MemoryID":"\([^"]*\)".*/\1/p')"
revision_id="$(printf '%s' "$add_json" | sed -n 's/.*"RevisionID":"\([^"]*\)".*/\1/p')"
[ -n "$memory_id" ] && [ -n "$revision_id" ] || { echo 'memory add did not return IDs' >&2; exit 1; }
talaria-mem memory confirm \
  --memory "$memory_id" \
  --expected-revision "$revision_id" \
  --json
talaria-mem memory search --workspace my-repo --query locally --json
talaria-mem projection rebuild --workspace my-repo --json
```

Valid kinds are `state`, `procedure`, `failure`, and `standing_instruction`.
Writes are unverified by default; an unverified memory intentionally does not
enter FTS search or SessionStart context. Confirmation creates a new revision.

## Reproducible smoke test

Before changing the operator's default root, use temporary managed directories:

```sh
root="$(mktemp -d)"
mkdir -p "$root/state" "$root/config" "$root/backups" "$root/codex"
chmod 700 "$root" "$root/state" "$root/config" "$root/backups" "$root/codex"
trap 'if [ -n "${daemon_pid:-}" ]; then kill "$daemon_pid" 2>/dev/null || true; wait "$daemon_pid" 2>/dev/null || true; fi; rm -rf "$root"' EXIT
export TALARIA_STATE_DIR="$root/state"
export TALARIA_CONFIG_DIR="$root/config"
export TALARIA_BACKUP_DIR="$root/backups"
talaria-mem setup codex --dry-run --json --endpoint 127.0.0.1:8743 \
  --codex-hooks "$root/codex/hooks.json"
talaria-mem setup codex --apply --json --endpoint 127.0.0.1:8743 \
  --codex-hooks "$root/codex/hooks.json"
test -f "$root/codex/hooks.json"
grep -Fq 'session-start.sh' "$root/codex/hooks.json"
talaria-mem daemon --foreground --address 127.0.0.1:8743 &
daemon_pid=$!
for attempt in $(seq 1 30); do
  curl --fail --silent --show-error --noproxy '*' http://127.0.0.1:8743/healthz >/dev/null && break
  [ "$attempt" -lt 30 ] || { echo 'daemon did not become healthy' >&2; exit 1; }
  sleep 0.1
done
talaria-mem status --json
talaria-mem doctor --json
talaria-mem workspace create --name smoke --json
talaria-mem workspace bind --key "path:$PWD" --workspace smoke --json
add_json="$(talaria-mem memory add --workspace smoke --kind state \
  --title 'Deployment smoke' --content 'Talaria-Mem is running locally' --json)"
memory_id="$(printf '%s' "$add_json" | sed -n 's/.*"MemoryID":"\([^"]*\)".*/\1/p')"
revision_id="$(printf '%s' "$add_json" | sed -n 's/.*"RevisionID":"\([^"]*\)".*/\1/p')"
[ -n "$memory_id" ] && [ -n "$revision_id" ] || { echo 'memory add did not return IDs' >&2; exit 1; }
talaria-mem memory confirm --memory "$memory_id" --expected-revision "$revision_id" --json
talaria-mem memory search --workspace smoke --query locally --json
talaria-mem projection rebuild --workspace smoke --json
printf '%s\n' "{\"session_id\":\"smoke-session\",\"cwd\":\"$PWD\",\"hook_event_name\":\"SessionStart\",\"transcript_path\":\"/tmp/should-not-be-read.jsonl\"}" \
  | "$root/config/session-start.sh" >"$root/hook-response.json"
! grep -Fq 'should-not-be-read' "$root/hook-response.json"
grep -Fq 'hookSpecificOutput' "$root/hook-response.json"
```

The trap stops the temporary daemon and removes only the exact temporary root.
If you stop copying the block before its final command, stop the background
process and remove that exact root manually; never use a broad recursive delete
against a home or workspace directory.

## Verified host installation and stress evidence

The current checkout was installed and exercised on a macOS arm64 host on
2026-08-19 (local host date) with Go 1.26.6, Codex CLI 0.144.5, and source
commit `76d7cd6` (`fix: accept historical migration journal targets`). This is
operator evidence for one host, not release evidence.

The existing `$HOME/.talaria-mem` root and LaunchAgent were upgraded in place.
The upgrade preserved the root key, bearer token, and SQLite database, applied
the new setup fingerprint, and left a second `setup codex --dry-run --json`
with `changes: []`. The existing daemon ran on `127.0.0.1:7437`; `/healthz`,
`status`, and `doctor` were healthy after the migration to schema target 4,
with queue depth `0`, no running curation jobs, and matching FTS rows and
content hashes. The `talaria_mem` MCP registration and unrelated Codex MCP
configuration remained in place.

The host checks included:

- `make check` after the migration fix.
- `go test -race -count=3 ./internal/curation ./internal/providers/codex ./internal/adapters/mcp ./internal/lifecycle`.
- `sh scripts/check-codex-schema.sh`, with Codex CLI 0.144.5.
- 100 concurrent `SessionStart` hook preflights, with no transcript or unknown-field leakage.
- `UserPromptSubmit` preflight plus fail-closed cases for an unsupported event, duplicate JSON keys, and a payload over 1 MiB.
- 20 serialized status checks and 20 serialized MCP proxy sessions; each session exposed all eight memory tools.

A cold-start burst of one-shot MCP processes produced `maintenance lock is
held` responses. The lock released normally; serialized retries then passed
20/20. This is the fail-closed contention behavior, not a stuck lock. The
stress-created one-shot processes were cleaned up individually, while the
pre-existing Codex app-server process was left untouched.

No prompt reached the enqueue threshold during this run, so no live provider
inference or paid curation was triggered. The record therefore validates the
local installation, lifecycle, hook boundary, and MCP transport; it does not
claim provider-output quality or a full Codex session-runner test. On this host,
replacing the executable also required an ad-hoc macOS signature to run at the
existing path; that is a host workaround, not release signing.

## Troubleshooting

| Symptom | Check first | Corrective action |
|---|---|---|
| `daemon unavailable` from the hook | `curl .../healthz` and `talaria-mem status` | Start the daemon and use the same endpoint in setup and daemon. |
| Hook returns `401` or `403` | `stat` the token file; do not print it | Re-run setup or confirm the hook and daemon use the same private root. |
| Hook returns `404` | Compare the hook `cwd` with `workspace bind --key "path:<cwd>"` | Bind the exact absolute Codex working directory. |
| Hook returns `400` | `talaria-mem --help` and the generated hook | Reinstall the hook from the same binary as the daemon. |
| `Codex hooks path is unsafe` | `stat -f '%Sp %Su' "$HOME/.codex"` and check the selected `--codex-hooks` path | Use an existing owner-owned parent without group/world write bits and a regular non-symlink JSON file. |
| `Codex setup fingerprint collision` | Review the existing command in `/hooks` and `hooks.json` | Preserve the user entry or remove the exact changed entry manually, then rerun setup. |
| `Codex hooks configuration is invalid` | Validate the selected JSON file with a local parser | Fix malformed or duplicate JSON keys; setup never overwrites invalid configuration. |
| `TALARIA_*_DIR` parsing fails | `env | grep '^TALARIA_'` without printing secrets | Unset all three variables for defaults, or set all three to existing safe paths. |
| Service starts then exits | `talaria-mem doctor` and platform logs | Confirm the template uses the same binary, default paths, and loopback port. |

## Documentation and integration gaps found

These remain visible after the contract and smoke fixes:

1. LaunchAgent/systemd templates exist, but there is no installer, placeholder
   substitution command, lifecycle verification, log location, or supported
   rollback procedure.
2. The repository has no committed end-to-end test that launches the Codex hook
   engine itself; the temporary daemon smoke covers the real hook payload and
   transport, not Codex's full session runner.
3. The source-build path has no release artifact, checksum, upgrade, uninstall,
   or backup/restore operator guide. Restore and scanner-rule upgrade are still
   unavailable in the composed CLI.
4. Registration currently targets the JSON hooks registry only. A consumer
   using `config.toml` or repository-local hooks must provide a separate
   integration path and trust review.

The supported boundary remains local and single-user. Outbound inference occurs
only through the configured provider chain; no unconfigured network destination
or cloud transcript extraction is used. Do not add cloud transcript extraction
to make installation appear easier.
