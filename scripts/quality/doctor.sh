#!/bin/sh
set -eu

EXPECTED_GOVERSION="${EXPECTED_GOVERSION:-go1.26}"
EXPECTED_GOFUMPT_VERSION="${EXPECTED_GOFUMPT_VERSION:-v0.9.2}"
EXPECTED_GOLANGCI_LINT_VERSION="${EXPECTED_GOLANGCI_LINT_VERSION:-v1.64.8}"
EXPECTED_MOCKGEN_VERSION="${EXPECTED_MOCKGEN_VERSION:-v0.6.0}"
EXPECTED_GOVULNCHECK_VERSION="${EXPECTED_GOVULNCHECK_VERSION:-v1.1.4}"
EXPECTED_PRE_COMMIT_VERSION="${EXPECTED_PRE_COMMIT_VERSION:-4.5.1}"

GOFUMPT_BIN="${GOFUMPT_BIN:-.cache/tools/bin/gofumpt}"
GOLANGCI_LINT_BIN="${GOLANGCI_LINT_BIN:-.cache/tools/bin/golangci-lint}"
MOCKGEN_BIN="${MOCKGEN_BIN:-.cache/tools/bin/mockgen}"
GOVULNCHECK_BIN="${GOVULNCHECK_BIN:-.cache/tools/bin/govulncheck}"
PRE_COMMIT_BIN="${PRE_COMMIT_BIN:-pre-commit}"
TOOLS_VERSION_DIR="${TOOLS_VERSION_DIR:-.cache/tools/versions}"

status=0

require_bin() {
	name="$1"
	bin="$2"
	if [ "${bin#*/}" != "$bin" ]; then
		if [ ! -x "$bin" ]; then
			echo "doctor: missing $name binary at $bin (run 'make tools')" >&2
			status=1
		fi
		return
	fi

	if ! command -v "$bin" >/dev/null 2>&1; then
		echo "doctor: missing $name command '$bin'" >&2
		status=1
	fi
}

check_contains() {
	name="$1"
	output="$2"
	expected="$3"
	if ! printf "%s" "$output" | grep -F "$expected" >/dev/null 2>&1; then
		echo "doctor: $name version mismatch: expected token '$expected', got '$output'" >&2
		status=1
	fi
}

check_contains_or_marker() {
	name="$1"
	output="$2"
	expected="$3"
	marker_file="$4"

	if printf "%s" "$output" | grep -F "$expected" >/dev/null 2>&1; then
		return
	fi

	if printf "%s" "$output" | grep -F "(devel)" >/dev/null 2>&1; then
		if [ -f "$marker_file" ] && [ "$(cat "$marker_file")" = "$expected" ]; then
			echo "doctor: $name built from local cached source; marker confirms $expected"
			return
		fi
	fi

	echo "doctor: $name version mismatch: expected token '$expected', got '$output'" >&2
	status=1
}

go_version="$(go env GOVERSION)"
case "$go_version" in
	"$EXPECTED_GOVERSION".*|"$EXPECTED_GOVERSION")
		;;
	*)
		echo "doctor: Go version mismatch: expected ${EXPECTED_GOVERSION}.x, got $go_version" >&2
		status=1
		;;
esac

require_bin "gofumpt" "$GOFUMPT_BIN"
require_bin "golangci-lint" "$GOLANGCI_LINT_BIN"
require_bin "mockgen" "$MOCKGEN_BIN"
require_bin "pre-commit" "$PRE_COMMIT_BIN"

if [ "$status" -eq 0 ]; then
	check_contains_or_marker \
		"gofumpt" \
		"$("$GOFUMPT_BIN" -version 2>&1)" \
		"$EXPECTED_GOFUMPT_VERSION" \
		"$TOOLS_VERSION_DIR/gofumpt.version"
	check_contains_or_marker \
		"golangci-lint" \
		"$("$GOLANGCI_LINT_BIN" version 2>&1)" \
		"$EXPECTED_GOLANGCI_LINT_VERSION" \
		"$TOOLS_VERSION_DIR/golangci-lint.version"
	check_contains_or_marker \
		"mockgen" \
		"$("$MOCKGEN_BIN" -version 2>&1)" \
		"$EXPECTED_MOCKGEN_VERSION" \
		"$TOOLS_VERSION_DIR/mockgen.version"
	check_contains "pre-commit" "$("$PRE_COMMIT_BIN" --version 2>&1)" "$EXPECTED_PRE_COMMIT_VERSION"
fi

if [ -x "$GOVULNCHECK_BIN" ]; then
	check_contains_or_marker \
		"govulncheck" \
		"$("$GOVULNCHECK_BIN" -version 2>&1)" \
		"govulncheck@$EXPECTED_GOVULNCHECK_VERSION" \
		"$TOOLS_VERSION_DIR/govulncheck.version"
else
	echo "doctor: warning: govulncheck not found at $GOVULNCHECK_BIN (security-warn remains non-blocking)" >&2
fi

if [ "$status" -ne 0 ]; then
	exit 1
fi

echo "doctor: environment checks passed"
