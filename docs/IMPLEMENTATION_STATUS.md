# Implementation status

This page describes the current checkout, not a release promise. Recheck the
working tree and rerun the linked commands before publishing a version. The
architecture specification and implementation plan remain target documents;
they describe capabilities that may still need composition or end-to-end
evidence.

| Capability | Current surface | Evidence status | Remaining work |
|---|---|---|---|
| Build, tests, race, vet | One Go module and `cmd/talaria-mem` binary | Run `make check` on the current checkout | Refresh release evidence after the final candidate is frozen |
| First-install paths | `setup codex --dry-run` safely creates the private root and managed leaves | `internal/runtime` setup tests | Keep platform-specific installation separate from path setup |
| Setup artifacts | Root key, bearer token, managed metadata, and executable SessionStart hook | `TestRunSetupApplyInstallsHookAndCredentials` | Register the hook with the host and add platform service installers |
| SessionStart contract | Hook sends raw Codex event fields; HTTP schema and adapter use the same fields | HTTP contract tests, `TestCompositionSessionStartListsBoundVerifiedMemory`, and hook packaging tests | Add a committed daemon-plus-hook smoke and full process-level egress evidence |
| CLI memory flow | Workspace create/list/bind; add, confirm, search, get, forget, status, doctor | Package and composition tests | Wire review listing, restore, and the remaining maintenance commands |
| HTTP/MCP reads | Unauthenticated loopback liveness; authenticated readiness, SessionStart, search, get, explain, and MCP reader | Adapter and composition tests | Add full process-level egress evidence |
| SQLite and projection | SQLite is canonical; Markdown is a rebuildable projection | SQLite/projection tests | Keep recovery and lifecycle evidence current |
| Backup reconciliation | `db backup reconcile --dry-run/--apply` is composed | Maintenance tests | Compose restore only after its scanner/lock contract is wired |
| Scanner rule upgrade | Library contracts exist; CLI registration is intentionally unavailable | `scanner rules upgrade` returns unavailable | Add the operator and activation lifecycle |
| Acceptance catalog | G00–G30 files and receipts exist as historical scaffolding | Metadata checks only; not release evidence for this checkout | Execute every catalog command and bind fresh raw output to the frozen candidate |
| Platform lifecycle | macOS/Linux templates are present | No supported end-to-end installer yet | Implement and test LaunchAgent/systemd-user installation |

The absence of a composed command is fail-closed: the binary reports
`command unavailable` rather than silently mutating local state.
