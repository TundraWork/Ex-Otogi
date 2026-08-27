SHELL := /bin/sh
GO := ./scripts/quality/go.sh
GOFMT := ./scripts/quality/format.sh
GOLANGCI_LINT := ./scripts/quality/golangci-lint.sh
SMOKE_COMMAND := $(GO) run ./cmd/otogi
GO_FILES := $(shell find . -type f -name '*.go' -not -path './.git/*' -not -path './.cache/*' -not -path './scripts/quality/testdata/*')

# Experimental milestone gates; these are not the full release gate.
.PHONY: format fmt-check lint arch-check test test-race test-leak smoke quality

format:
	$(GOFMT) -w $(GO_FILES)

fmt-check:
	@output="$$($(GOFMT) -l $(GO_FILES))"; status=$$?; [ $$status -eq 0 ] || exit $$status; [ -z "$$output" ] || { printf '%s\n' "$$output"; exit 1; }

lint:
	$(GOLANGCI_LINT) run ./...
	$(GO) run ./scripts/quality/cmd/doccheck

arch-check:
	$(GO) run ./scripts/quality/cmd/archcheck

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-leak:
	$(GO) test ./internal/kernel ./internal/adapter/scripted

smoke:
	@output="$$($(SMOKE_COMMAND))"; status=$$?; [ $$status -eq 0 ] || exit $$status; [ "$$output" = "echo: hello" ]

quality: fmt-check lint arch-check test test-race test-leak smoke
