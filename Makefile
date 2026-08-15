GO ?= go
BUILD_DIR ?= $(CURDIR)/.build
OPENAPI_SOURCE := api/openapi/talaria.yaml
OPENAPI_OVERLAY := api/openapi/vacuum.yaml
OPENAPI_GENERATED := internal/adapters/http/openapi.gen.go

.PHONY: build test test-race vet generate openapi-lint source-tree-clean check clean

build:
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build -o "$(BUILD_DIR)/talaria-mem" ./cmd/talaria-mem

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

generate:
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

clean:
	@rm -rf "$(BUILD_DIR)"
