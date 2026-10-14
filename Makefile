SHELL := /bin/sh
GO ?= go
LINT := $(GO) tool -modfile=tools/go.mod golangci-lint
TELEGRAM_FUZZTIME ?= 1x

.PHONY: format lint test test-race test-fuzz-smoke smoke telegram-smoke quality

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
	$(GO) test -run '^$$' -fuzz '^FuzzConvertEntitiesUTF16$$' -fuzztime=$(TELEGRAM_FUZZTIME) ./internal/adapter/telegram/mapper

smoke:
	@output="$$($(GO) run ./cmd/otogi smoke)" && [ "$$output" = "echo: hello" ] || { printf 'smoke: got %s\n' "$$output"; exit 1; }

# Opt-in live check against Telegram; never part of quality.
telegram-smoke:
	$(GO) run ./cmd/telegram-smoke

quality: lint test-race test-fuzz-smoke smoke
