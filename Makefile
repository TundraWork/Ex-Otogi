SHELL := /bin/sh

GO ?= go
MAIN_PKG ?= ./cmd/bot
BUILD_OUT ?= $(CURDIR)/bin/bot
TOOLS_DIR ?= $(CURDIR)/.cache/tools
TOOLS_BIN ?= $(TOOLS_DIR)/bin
TOOLS_VERSION_DIR ?= $(TOOLS_DIR)/versions
GOLANGCI_LINT_CACHE ?= $(CURDIR)/.cache/golangci-lint
GO_BUILD_CACHE ?= $(CURDIR)/.cache/go-build
COVERAGE_DIR ?= $(CURDIR)/.cache/coverage

GOFUMPT_VERSION ?= v0.9.2
GOLANGCI_LINT_VERSION ?= v1.64.8
MOCKGEN_VERSION ?= v0.6.0
GOVULNCHECK_VERSION ?= v1.1.4
PRE_COMMIT_VERSION ?= 4.5.1
PRE_COMMIT_HOME_DIR ?= $(CURDIR)/.cache/pre-commit

GOFUMPT ?= $(TOOLS_BIN)/gofumpt
GOLANGCI_LINT ?= $(TOOLS_BIN)/golangci-lint
MOCKGEN ?= $(TOOLS_BIN)/mockgen
GOVULNCHECK ?= $(TOOLS_BIN)/govulncheck
PRE_COMMIT_CMD ?= pre-commit

QUALITY_SCRIPTS_DIR ?= $(CURDIR)/scripts/quality
DOCTOR_SCRIPT ?= $(QUALITY_SCRIPTS_DIR)/doctor.sh
ARCHCHECK_PKG ?= ./scripts/quality/archcheck
COVERAGE_SCRIPT ?= $(QUALITY_SCRIPTS_DIR)/coverage_report.sh
SECURITY_WARN_SCRIPT ?= $(QUALITY_SCRIPTS_DIR)/security_warn.sh
AGENT_GUARD_SCRIPT ?= $(QUALITY_SCRIPTS_DIR)/agent_guard.sh
TEST_EXCEPTION_FILE ?= $(CURDIR)/config/quality/test_required_exceptions.txt

.PHONY: tools doctor quality quality-core fmt fmt-check lint arch-check \
	test test-race test-leak coverage-report security-warn agent-guard \
	build dev generate hooks-install hooks-run

tools: ## Install pinned local development tooling under .cache/tools/bin
	@mkdir -p $(TOOLS_BIN) $(TOOLS_VERSION_DIR) $(GOLANGCI_LINT_CACHE) $(GO_BUILD_CACHE) $(COVERAGE_DIR)
	@set -eu; \
	if ! GOCACHE=$(GO_BUILD_CACHE) GOBIN=$(TOOLS_BIN) $(GO) install mvdan.cc/gofumpt@$(GOFUMPT_VERSION); then \
		cache_dir="$$( $(GO) env GOPATH )/pkg/mod/mvdan.cc/gofumpt@$(GOFUMPT_VERSION)"; \
		echo "tools: falling back to cached module $$cache_dir"; \
		[ -d "$$cache_dir" ] || (echo "tools: missing cached module $$cache_dir"; exit 1); \
		(cd "$$cache_dir" && GOCACHE=$(GO_BUILD_CACHE) GOBIN=$(TOOLS_BIN) $(GO) install .); \
	fi; \
	echo "$(GOFUMPT_VERSION)" > "$(TOOLS_VERSION_DIR)/gofumpt.version"
	@set -eu; \
	if ! GOCACHE=$(GO_BUILD_CACHE) GOBIN=$(TOOLS_BIN) $(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); then \
		cache_dir="$$( $(GO) env GOPATH )/pkg/mod/github.com/golangci/golangci-lint@$(GOLANGCI_LINT_VERSION)"; \
		echo "tools: falling back to cached module $$cache_dir"; \
		[ -d "$$cache_dir" ] || (echo "tools: missing cached module $$cache_dir"; exit 1); \
		(cd "$$cache_dir" && GOCACHE=$(GO_BUILD_CACHE) GOBIN=$(TOOLS_BIN) $(GO) install ./cmd/golangci-lint); \
	fi; \
	echo "$(GOLANGCI_LINT_VERSION)" > "$(TOOLS_VERSION_DIR)/golangci-lint.version"
	@set -eu; \
	if ! GOCACHE=$(GO_BUILD_CACHE) GOBIN=$(TOOLS_BIN) $(GO) install go.uber.org/mock/mockgen@$(MOCKGEN_VERSION); then \
		cache_dir="$$( $(GO) env GOPATH )/pkg/mod/go.uber.org/mock@$(MOCKGEN_VERSION)"; \
		echo "tools: falling back to cached module $$cache_dir"; \
		[ -d "$$cache_dir" ] || (echo "tools: missing cached module $$cache_dir"; exit 1); \
		(cd "$$cache_dir" && GOCACHE=$(GO_BUILD_CACHE) GOBIN=$(TOOLS_BIN) $(GO) install ./mockgen); \
	fi; \
	echo "$(MOCKGEN_VERSION)" > "$(TOOLS_VERSION_DIR)/mockgen.version"
	@set -eu; \
	govulncheck_installed=0; \
	if ! GOCACHE=$(GO_BUILD_CACHE) GOBIN=$(TOOLS_BIN) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); then \
		cache_dir="$$( $(GO) env GOPATH )/pkg/mod/golang.org/x/vuln@$(GOVULNCHECK_VERSION)"; \
		echo "tools: falling back to cached module $$cache_dir"; \
		if [ -d "$$cache_dir" ]; then \
			if ! (cd "$$cache_dir" && GOCACHE=$(GO_BUILD_CACHE) GOBIN=$(TOOLS_BIN) $(GO) install ./cmd/govulncheck); then \
				echo "tools: warning: govulncheck install failed; security-warn will continue in warning-only mode"; \
			else \
				govulncheck_installed=1; \
			fi; \
		else \
			echo "tools: warning: missing cached module $$cache_dir; security-warn will continue in warning-only mode"; \
		fi; \
	else \
		govulncheck_installed=1; \
	fi; \
	if [ "$$govulncheck_installed" -eq 1 ]; then \
		echo "$(GOVULNCHECK_VERSION)" > "$(TOOLS_VERSION_DIR)/govulncheck.version"; \
	else \
		rm -f "$(TOOLS_VERSION_DIR)/govulncheck.version"; \
	fi
	@command -v $(PRE_COMMIT_CMD) >/dev/null 2>&1 || \
		(echo "pre-commit $(PRE_COMMIT_VERSION)+ required (install via pipx install pre-commit)"; exit 1)

doctor: ## Verify Go/toolchain and pinned tool versions
	@EXPECTED_GOVERSION=go1.26 \
	EXPECTED_GOFUMPT_VERSION=$(GOFUMPT_VERSION) \
	EXPECTED_GOLANGCI_LINT_VERSION=$(GOLANGCI_LINT_VERSION) \
	EXPECTED_MOCKGEN_VERSION=$(MOCKGEN_VERSION) \
	EXPECTED_GOVULNCHECK_VERSION=$(GOVULNCHECK_VERSION) \
	EXPECTED_PRE_COMMIT_VERSION=$(PRE_COMMIT_VERSION) \
	GOFUMPT_BIN=$(GOFUMPT) \
	GOLANGCI_LINT_BIN=$(GOLANGCI_LINT) \
	MOCKGEN_BIN=$(MOCKGEN) \
	GOVULNCHECK_BIN=$(GOVULNCHECK) \
	TOOLS_VERSION_DIR=$(TOOLS_VERSION_DIR) \
	PRE_COMMIT_BIN=$(PRE_COMMIT_CMD) \
	$(DOCTOR_SCRIPT)

quality: ## Canonical local quality gate (hard + report-only checks)
	$(MAKE) quality-core
	$(MAKE) coverage-report
	$(MAKE) security-warn

quality-core: ## Hard-fail quality gate for merges/commits
	$(MAKE) doctor
	$(MAKE) fmt-check
	$(MAKE) lint
	$(MAKE) arch-check
	$(MAKE) agent-guard
	$(MAKE) test-race
	$(MAKE) test-leak

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

lint: ## Run static analysis with pinned golangci-lint
	@mkdir -p $(GOLANGCI_LINT_CACHE) $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) run ./...

build: ## Build the bot binary
	@mkdir -p $(dir $(BUILD_OUT))
	$(GO) build -o $(BUILD_OUT) $(MAIN_PKG)

arch-check: ## Enforce dependency direction for core layers via package graph
	@mkdir -p $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) $(GO) run $(ARCHCHECK_PKG)

test: test-race ## Alias for race-enabled test suite

test-race: ## Run all tests with race detector (mandatory)
	@mkdir -p $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) $(GO) test -race ./...

test-leak: ## Run goroutine leak checks for concurrency-heavy packages
	@mkdir -p $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) $(GO) test -tags goleak ./internal/kernel ./internal/driver/telegram ./modules/llmchat

coverage-report: ## Generate report-only coverage artifacts under .cache/coverage
	@mkdir -p $(COVERAGE_DIR) $(GO_BUILD_CACHE)
	COVERAGE_DIR=$(COVERAGE_DIR) GO_BUILD_CACHE=$(GO_BUILD_CACHE) $(COVERAGE_SCRIPT)

security-warn: ## Run non-blocking security checks and warnings
	GOVULNCHECK_BIN=$(GOVULNCHECK) $(SECURITY_WARN_SCRIPT)

agent-guard: ## Enforce LLM-focused changed-code policy checks
	TEST_EXCEPTION_FILE=$(TEST_EXCEPTION_FILE) QUALITY_DIFF_MODE=$${QUALITY_DIFF_MODE:-working-tree} $(AGENT_GUARD_SCRIPT)

hooks-install: ## Install local pre-commit hook
	@mkdir -p $(PRE_COMMIT_HOME_DIR)
	PRE_COMMIT_HOME=$(PRE_COMMIT_HOME_DIR) $(PRE_COMMIT_CMD) install --hook-type pre-commit

hooks-run: ## Run pre-commit on all files
	@mkdir -p $(PRE_COMMIT_HOME_DIR)
	PRE_COMMIT_HOME=$(PRE_COMMIT_HOME_DIR) $(PRE_COMMIT_CMD) run --all-files

generate: ## Run go:generate for mocks and generated code
	PATH=$(TOOLS_BIN):$$PATH $(GO) generate ./...

dev: ## Run with hot reload when air is available
	@if command -v air >/dev/null 2>&1; then \
		if [ -f .air.toml ]; then air -c .air.toml; else air; fi; \
	else \
		echo "air not found; running without hot reload"; \
		$(GO) run $(MAIN_PKG); \
	fi
