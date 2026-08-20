# Implementation status

This page describes the current unreleased automatic-memory checkout, not a
release promise. Recheck the working tree and rerun the linked commands before
publishing a version. Live Codex/Luna inference remains opt-in; ordinary tests
use local fakes and never require provider access.

| Capability | Current surface | Evidence status | Remaining work |
|---|---|---|---|
| Build, tests, race, vet | One Go module and `cmd/talaria-mem` binary | `go test ./...`, `go test -race ./...`, and `go vet ./...` pass for this candidate | Refresh release evidence after the final candidate is frozen; run the full `make check` from a clean generated tree |
| First-install paths | `setup codex --dry-run` safely creates the private root and reports the official Codex registration | `internal/runtime` setup tests and lifecycle installer tests | Keep platform-specific service installation separate from path and hook setup |
| Setup artifacts | Root key, bearer token, managed metadata, generic four-event hook, collision-safe `hooks.json` groups, MCP registration, and absent-only provider defaults | `internal/lifecycle/codex_hooks_test.go`, MCP/provider installer tests, `TestRunSetupApplyInstallsHookAndCredentials` | Add platform service installers and release upgrade/uninstall tooling |
| Automatic curation | Encrypted bounded queue, generated trust, bounded Codex `thread/read` snapshots, ordered Codex/compatible providers, retries, expiry, and generated-memory dedupe | `internal/runtime/curation.go`, `internal/curation`, SQLite curation tests, `internal/acceptance/TestAutomaticMemoryLifecycle` | Add committed daemon-plus-hook smoke, live opt-in evidence, and queue-repair receipts |
| Prompt recall | UserPromptSubmit local FTS recall with generated labeling and bounded output | `internal/adapters/codex/recall_test.go`, runtime composition/acceptance tests | Measure relevance on representative workspaces |
| MCP curation | `memory_curate_inline` and authenticated `mcp proxy` stdio bridge | `internal/adapters/mcp` and CLI tests | Add a live Codex MCP consumer smoke |
| Safe curation health | Status/doctor report provider order/availability, queue depth, oldest safe age, running count, and safe error class | `internal/lifecycle/curation_health_test.go`, runtime composition tests | Add repair receipts for persistent queue corruption |
| SessionStart contract | Hook accepts the official Codex event and forwards bounded `session_id`, `cwd`, and `hook_event_name` fields | HTTP contract tests, `TestCompositionSessionStartListsBoundVerifiedMemory`, and hook packaging tests | Add a committed daemon-plus-hook smoke and full process-level egress evidence |
| CLI surface | Command tree with generated help, persistent `--json`, typed flags, and usage exit code `2` | `internal/cli/root_test.go`, command tests, and `cmd/talaria-mem/main_test.go` | Keep command help and the operator runbook aligned as commands become available |
| CLI memory flow | Workspace create/list/show/bind/merge; memory add, update, confirm, pin, forget, restore, review, search, get, and explain; status and doctor | Package and composition tests; review-listing remains fail-closed until its paginated client is composed | Compose review listing and the remaining maintenance commands |
| HTTP/MCP reads | Unauthenticated loopback liveness; authenticated readiness, SessionStart, search, get, explain, and MCP reader | Adapter and composition tests | Add full process-level egress evidence |
| Environment boundary | Typed `TALARIA_STATE_DIR`, `TALARIA_CONFIG_DIR`, and `TALARIA_BACKUP_DIR`; default private root when all are unset | Generated `internal/lifecycle/environment.md` and lifecycle tests | Keep generated environment documentation synchronized with parser behavior |
| SQLite and projection | SQLite is canonical; Markdown is a rebuildable projection | SQLite/projection tests | Keep recovery and lifecycle evidence current |
| Backup reconciliation | `db backup reconcile --dry-run/--apply` is composed | Maintenance tests | Compose database restore only after its scanner/lock contract is wired |
| Scanner rule upgrade | Library contracts exist; CLI registration is intentionally unavailable | `scanner rules upgrade` returns unavailable | Add the operator and activation lifecycle |
| Acceptance catalog | G00–G30 files and receipts exist as historical scaffolding | Metadata checks only; not release evidence for this checkout | Execute every catalog command and bind fresh raw output to the frozen candidate |
| Local deployment runbook | Source build, private setup, daemon verification, consumer Codex registration, hook/MCP recipes, smoke test, and troubleshooting | [`docs/LOCAL_DEPLOYMENT.md`](LOCAL_DEPLOYMENT.md) records the verified path and explicit gaps | Add release artifacts, upgrade/uninstall, backup/restore, and supported host installers |
| Platform lifecycle | macOS/Linux templates are present | No supported end-to-end installer yet; templates are manual recipes only | Implement and test LaunchAgent/systemd-user installation |

The absence of a composed command is fail-closed: the binary reports
`command unavailable` rather than silently mutating local state.

The runbook's service-manager section remains an integration recipe: `setup`
registers the official JSON Codex hook, but does not start a daemon or install a
service. A third-party deployment must preserve that distinction until the
platform installers and end-to-end Codex runner test exist.
