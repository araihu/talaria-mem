# Talaria-Mem OpenAPI

`talaria.yaml` is the versioned loopback control contract. `vacuum.yaml` is the
repository-owned Vacuum configuration. The source is bundled and linted before
any generated HTTP boundary is refreshed:

```sh
vacuum lint api/openapi/talaria.yaml --config api/openapi/vacuum.yaml
vacuum bundle api/openapi/talaria.yaml /tmp/talaria-mem-openapi.bundle.yaml
go generate ./...
```

`make openapi-lint` installs the pinned Vacuum version when it is not already
available, so a cold tool cache may need network access. The generated Go file
is checked into the repository; normal runtime operation does not invoke a
generator or contact a service. Do not put bearer tokens, content, or scanner
findings in this directory.

The SessionStart request mirrors the raw Codex event. Unknown event fields are
accepted at this boundary and discarded before application dispatch; transcript
fields are never opened, logged, or persisted.
