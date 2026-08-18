# Talaria-Mem task list

This is the active implementation list for the consumer deployment path. It
complements [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md): a checked
item means the code and focused tests exist, not that a release is published.

## Completed in the current local-install slice

- [x] Use Cobra for the public CLI and generated help.
- [x] Let `setup codex --dry-run` report both managed Talaria-Mem metadata and
  the official Codex hook registration.
- [x] Make registration idempotent and preserve unrelated `hooks.json`
  settings and hook groups.
- [x] Refuse changed commands, duplicate managed entries, unsafe paths,
  malformed JSON, and duplicate JSON keys without echoing configuration
  content.
- [x] Roll back the official registry when the generated hook collides.
- [x] Cover setup receipts and diagnostics with credential/non-content canaries.

## Open, ordered by consumer impact

- [ ] Add a supported macOS LaunchAgent installer with placeholder-free
  configuration, status verification, upgrade, and rollback.
- [ ] Add a supported Linux `systemd --user` installer with the same lifecycle
  contract.
- [ ] Add a committed end-to-end test that runs the real Codex hook runner, not
  only the official payload against the local HTTP endpoint.
- [ ] Publish versioned native artifacts, checksums, upgrade instructions, and
  an uninstall/restore procedure.
- [ ] Add a consumer upgrade test from an older generated hook and an older
  `hooks.json` registration while preserving user edits.
- [ ] Finish the privacy evidence matrix for CLI, HTTP, MCP, hook stderr, and
  service-manager logs using a real log sink; assert token/root-key and
  submitted-content absence from every diagnostic channel.
- [ ] Re-run concurrent hook, health, restart, and workspace-persistence soak
  evidence against the release candidate.

## Deferred scope

- [ ] Keep transcript ingestion, cloud extraction, embeddings, and automatic
  memory writes out of the consumer installer unless separately specified and
  reviewed.
