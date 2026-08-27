#!/bin/sh
set -eu
unset GOROOT
export GOTOOLCHAIN=go1.27.0
exec go "$@"
