#!/bin/sh
set -eu
unset GOROOT
export GOTOOLCHAIN=go1.27.0
version=2.13.1
cache=${GOLANGCI_LINT_CACHE:-.cache/golangci-lint}
case $cache in
	/*) ;;
	*) cache="$(pwd)/$cache" ;;
esac
binary="$cache/golangci-lint-${version}"
if [ ! -x "$binary" ]; then
	mkdir -p "$(dirname "$binary")"
	GOBIN="$(cd "$(dirname "$binary")" && pwd)" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${version}"
	mv "$(dirname "$binary")/golangci-lint" "$binary"
fi
exec "$binary" "$@"
