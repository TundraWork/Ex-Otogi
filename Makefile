SHELL := /bin/sh

GO ?= go
MAIN_PKG ?= ./cmd/bot
BUILD_OUT ?= $(CURDIR)/bin/bot
GOLANGCI_LINT_CACHE ?= $(CURDIR)/.cache/golangci-lint
GO_BUILD_CACHE ?= $(CURDIR)/.cache/go-build
GOLANGCI_LINT ?= golangci-lint
GOFUMPT ?= gofumpt

.PHONY: tools fmt fmt-check lint build arch-check test dev generate

tools: ## Install local development and CI tooling
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	$(GO) install go.uber.org/mock/mockgen@latest
	$(GO) install github.com/air-verse/air@latest
	@command -v pre-commit >/dev/null 2>&1 && pre-commit install || \
		echo "pre-commit not found. Install with: pipx install pre-commit"

fmt: ## Format Go source files with gofumpt
	$(GOFUMPT) -w .

fmt-check: ## Verify Go source formatting with gofumpt
	@out="$$( $(GOFUMPT) -l . )"; \
	if [ -n "$$out" ]; then \
		echo "gofumpt formatting issues found:"; \
		echo "$$out"; \
		echo "run 'make fmt' to apply formatting"; \
		exit 1; \
	fi

lint: ## Run formatting, static analysis, and architecture checks
	$(MAKE) fmt-check
	@mkdir -p $(GOLANGCI_LINT_CACHE) $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) run ./...
	$(MAKE) arch-check

build: ## Build the bot binary
	@mkdir -p $(dir $(BUILD_OUT))
	$(GO) build -o $(BUILD_OUT) $(MAIN_PKG)

arch-check: ## Enforce dependency direction for core layers
	@! rg -n --glob '*.go' '".*\/internal\/driver' internal/kernel >/dev/null || \
		(echo "architecture violation: internal/kernel must not import internal/driver"; exit 1)
	@! rg -n --glob '*.go' '".*\/internal\/' pkg/otogi >/dev/null || \
		(echo "architecture violation: pkg/otogi must not import internal/*"; exit 1)

test: ## Run all tests with race detector (mandatory)
	$(GO) test -race ./...

generate: ## Run go:generate for mocks and generated code
	$(GO) generate ./...

dev: ## Run with hot reload when air is available
	@if command -v air >/dev/null 2>&1; then \
		if [ -f .air.toml ]; then air -c .air.toml; else air; fi; \
	else \
		echo "air not found; running without hot reload"; \
		$(GO) run $(MAIN_PKG); \
	fi
