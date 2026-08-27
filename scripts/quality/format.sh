#!/bin/sh
set -eu
unset GOROOT
export GOTOOLCHAIN=go1.27.0
gofmt="$(go env GOROOT)/bin/gofmt"
exec "$gofmt" "$@"
