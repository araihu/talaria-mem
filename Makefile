GO ?= go
SQLC_VERSION := v1.29.0
VACUUM_VERSION := v0.30.0
BUILD_DIR ?= $(CURDIR)/.build
OPENAPI_SOURCE := api/openapi/talaria.yaml
OPENAPI_OVERLAY := api/openapi/vacuum.yaml
OPENAPI_GENERATED := internal/adapters/http/openapi.gen.go
SCANNER_ENVIRONMENT_DOC := internal/scanner/environment.md
CODEX_PROTOCOL_GENERATED := internal/providers/codex/protocol

.PHONY: build test test-race vet sqlc-generate generate openapi-lint source-tree-clean check clean sqlc-negative

build:
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build -o "$(BUILD_DIR)/talaria-mem" ./cmd/talaria-mem

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

sqlc-generate:
	$(GO) run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate -f db/sqlc.yaml
	sh scripts/update-sqlc-manifest.sh

generate: sqlc-generate
	$(GO) generate ./...

openapi-lint:
	@if test -f "$(OPENAPI_SOURCE)"; then \
		vacuum_bin="$$(command -v vacuum 2>/dev/null || true)"; \
		if test -z "$$vacuum_bin"; then \
			$(GO) install github.com/daveshanley/vacuum@$(VACUUM_VERSION); \
			vacuum_bin="$$( $(GO) env GOPATH )/bin/vacuum"; \
		fi; \
		mkdir -p "$(BUILD_DIR)"; \
		"$$vacuum_bin" lint "$(OPENAPI_SOURCE)" --config "$(OPENAPI_OVERLAY)"; \
		"$$vacuum_bin" bundle "$(OPENAPI_SOURCE)" "$(BUILD_DIR)/talaria.openapi.yaml"; \
	fi

source-tree-clean:
	@test -z "$$(find . -type f \( -name talaria-mem -o -name '*.tmp' \) -not -path './.git/*' -not -path './.build/*' -print -quit)"

check: test test-race vet generate openapi-lint source-tree-clean
	@git diff --exit-code -- "$(OPENAPI_GENERATED)"
	@git diff --exit-code -- "$(SCANNER_ENVIRONMENT_DOC)"
	@git diff --exit-code -- internal/adapters/sqlite/sqlc
	@git diff --exit-code -- internal/adapters/sqlite/sqlc.manifest
	@git diff --exit-code -- "$(CODEX_PROTOCOL_GENERATED)"
	@sh scripts/check-sqlc-generated.sh

sqlc-negative:
	sh scripts/check-sqlc-generated-negative.sh

clean:
	@rm -rf "$(BUILD_DIR)"
