#!/bin/sh
set -eu

COVERAGE_DIR="${COVERAGE_DIR:-.cache/coverage}"
GO_BUILD_CACHE="${GO_BUILD_CACHE:-.cache/go-build}"
PROFILE_FILE="${COVERAGE_DIR}/coverage.out"
SUMMARY_FILE="${COVERAGE_DIR}/coverage.txt"

mkdir -p "$COVERAGE_DIR" "$GO_BUILD_CACHE"

packages="$(go list ./... | grep -v '^ex-otogi/scripts/' || true)"
if [ -z "$packages" ]; then
	echo "coverage-report: warning: no packages selected for coverage (non-blocking)" >&2
	exit 0
fi

echo "coverage-report: writing profile to $PROFILE_FILE"
if ! GOCACHE="$GO_BUILD_CACHE" go test -covermode=atomic -coverprofile="$PROFILE_FILE" $packages; then
	echo "coverage-report: warning: go test coverage run failed (non-blocking)" >&2
	exit 0
fi

if ! GOCACHE="$GO_BUILD_CACHE" go tool cover -func="$PROFILE_FILE" >"$SUMMARY_FILE"; then
	echo "coverage-report: warning: failed to summarize coverage profile (non-blocking)" >&2
	exit 0
fi

total_line="$(grep '^total:' "$SUMMARY_FILE" || true)"
if [ -n "$total_line" ]; then
	echo "coverage-report: $total_line"
else
	echo "coverage-report: summary generated at $SUMMARY_FILE"
fi
echo "coverage-report: profile=$PROFILE_FILE summary=$SUMMARY_FILE"
