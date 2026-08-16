# Talaria-Mem OpenAPI

`talaria.yaml` is the versioned loopback control contract. `vacuum.yaml` is
the repository-owned, offline Vacuum configuration. The source is bundled and
linted before any generated HTTP boundary is refreshed:

```sh
vacuum lint -r api/openapi/vacuum.yaml api/openapi/talaria.yaml
vacuum bundle api/openapi/talaria.yaml /tmp/talaria-mem-openapi.bundle.yaml
go generate ./...
```

The generated Go file is checked into the repository so a build does not need
network access or a generator at runtime. Do not put bearer tokens, content,
or scanner findings in this directory.
