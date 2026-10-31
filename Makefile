SHELL := /bin/sh
GO ?= go
LINT := $(GO) tool -modfile=tools/go.mod golangci-lint
FUZZTIME ?= 1x

# Test deployment (scripts/deploy.sh); override on the command line.
DEPLOY_HOST ?= misaka-lax01
DEPLOY_DIR ?= /srv/ex-otogi-next
DEPLOY_SERVICE ?= ex-otogi-next
DEPLOY_READY_URL ?= http://127.0.0.1:8383/readyz
DEPLOY_ENV := DEPLOY_HOST=$(DEPLOY_HOST) DEPLOY_DIR=$(DEPLOY_DIR) DEPLOY_SERVICE=$(DEPLOY_SERVICE) DEPLOY_READY_URL=$(DEPLOY_READY_URL)

.PHONY: format lint test test-race test-fuzz-smoke smoke telegram-smoke quality deploy deploy-rollback

format:
	$(LINT) fmt

# Formatting, static analysis, Godoc, and import boundaries (depguard).
lint:
	$(LINT) run ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-fuzz-smoke:
	$(GO) test -run '^$$' -fuzz '^FuzzConvertEntitiesUTF16$$' -fuzztime=$(FUZZTIME) ./internal/adapter/telegram/mapper
	$(GO) test -run '^$$' -fuzz '^FuzzFormat$$' -fuzztime=$(FUZZTIME) ./pkg/markdown

smoke:
	@output="$$($(GO) run ./cmd/otogi smoke)" && [ "$$output" = "echo: hello" ] || { printf 'smoke: got %s\n' "$$output"; exit 1; }

# Opt-in live check against Telegram; never part of quality.
telegram-smoke:
	$(GO) run ./cmd/telegram-smoke

quality: lint test-race test-fuzz-smoke smoke

# Deploy the current checkout to the test host, or restore its previous binary.
deploy:
	$(DEPLOY_ENV) ./scripts/deploy.sh

deploy-rollback:
	$(DEPLOY_ENV) ./scripts/deploy.sh rollback
