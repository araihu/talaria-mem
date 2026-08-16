# talaria-mem
Local-first, workspace-scoped memory for Codex.

## Local v0.0.1 journey

Build the single binary outside the source tree, then explicitly initialize
Codex integration:

```sh
go build -o /tmp/talaria-mem ./cmd/talaria-mem
/tmp/talaria-mem setup codex --dry-run
/tmp/talaria-mem setup codex --apply
/tmp/talaria-mem daemon --foreground
```

The daemon binds only to authenticated literal loopback. It has no cloud or
model-extraction path. `status`, `doctor`, `doctor --repair=fts --dry-run`,
`doctor --repair=fts --apply <receipt>`, and `token rotate` expose lifecycle
diagnostics and maintenance without returning stored secrets.

Final local gates:

```sh
go test ./...
go test -race ./...
go vet ./...
go generate ./...
make sqlc-generate
vacuum lint api/openapi/talaria.yaml --config api/openapi/vacuum.yaml
vacuum bundle api/openapi/talaria.yaml
go test ./test/acceptance -count=1
```

Acceptance gate IDs G00-G30 and requirement/section traceability live under
`test/acceptance/`. Deferred work remains in `ROADMAP.md`.
