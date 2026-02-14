SHELL := /bin/sh

GO ?= go
MAIN_PKG ?= ./cmd/bot
GOLANGCI_LINT_CACHE ?= $(CURDIR)/.cache/golangci-lint

.PHONY: tools lint arch-check test dev generate

tools: ## Install local development and CI tooling
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	$(GO) install go.uber.org/mock/mockgen@latest
	$(GO) install github.com/air-verse/air@latest
	@command -v pre-commit >/dev/null 2>&1 && pre-commit install || \
		echo "pre-commit not found. Install with: pipx install pre-commit"

lint: ## Run static analysis and architecture checks
	@mkdir -p $(GOLANGCI_LINT_CACHE)
	GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) golangci-lint run ./...
	$(MAKE) arch-check

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
