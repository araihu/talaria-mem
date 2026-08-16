GO ?= go
SQLC_VERSION := v1.29.0
BUILD_DIR ?= $(CURDIR)/.build
OPENAPI_SOURCE := api/openapi/talaria.yaml
OPENAPI_OVERLAY := api/openapi/vacuum.yaml
OPENAPI_GENERATED := internal/adapters/http/openapi.gen.go
SCANNER_ENVIRONMENT_DOC := internal/scanner/environment.md

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
		command -v vacuum >/dev/null; \
		vacuum lint "$(OPENAPI_SOURCE)" "$(OPENAPI_OVERLAY)"; \
		vacuum bundle "$(OPENAPI_SOURCE)"; \
	fi

source-tree-clean:
	@test -z "$$(find . -type f \( -name talaria-mem -o -name '*.tmp' \) -not -path './.git/*' -not -path './.build/*' -print -quit)"

check: test test-race vet generate openapi-lint source-tree-clean
	@git diff --exit-code -- "$(OPENAPI_GENERATED)"
	@git diff --exit-code -- "$(SCANNER_ENVIRONMENT_DOC)"
	@git diff --exit-code -- internal/adapters/sqlite/sqlc
	@git diff --exit-code -- internal/adapters/sqlite/sqlc.manifest
	@sh scripts/check-sqlc-generated.sh

sqlc-negative:
	sh scripts/check-sqlc-generated-negative.sh

clean:
	@rm -rf "$(BUILD_DIR)"
