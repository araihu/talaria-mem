# Talaria-Mem OpenAPI

`talaria.yaml` is the versioned loopback control contract. `vacuum.yaml` is the
repository-owned Vacuum configuration. The source is bundled and linted before
any generated HTTP boundary is refreshed:

```sh
make openapi-lint
go generate ./...
```

`make openapi-lint` writes the bundle to `.build/talaria.openapi.yaml`. The
`scripts/vacuum` wrapper downloads the official Vacuum v0.30.0 release on
first use. It verifies the archive against the pinned SHA-256 on every run
and caches it under `$XDG_CACHE_HOME/talaria-mem/vacuum` (default
`~/.cache/talaria-mem/vacuum`). The generated Go file
is checked into the repository; normal runtime operation does not invoke a
generator or contact a service. Do not put bearer tokens, content, or scanner
findings in this directory.

The SessionStart request mirrors the raw Codex event. Unknown event fields are
accepted at this boundary and discarded before application dispatch; transcript
fields are never opened, logged, or persisted.
