#!/bin/sh
set -eu

GOVULNCHECK_BIN="${GOVULNCHECK_BIN:-.cache/tools/bin/govulncheck}"

warn() {
	echo "security-warn: warning: $1" >&2
}

echo "security-warn: running non-blocking security checks"

if [ -x "$GOVULNCHECK_BIN" ]; then
	if ! "$GOVULNCHECK_BIN" ./...; then
		warn "govulncheck reported vulnerabilities or failed; review output above"
	fi
else
	warn "govulncheck not found at $GOVULNCHECK_BIN (run 'make tools')"
fi

secret_matches="$(
	git grep -nE \
		'(AKIA[0-9A-Z]{16}|-----BEGIN (RSA|EC|OPENSSH|DSA) PRIVATE KEY-----|sk-[A-Za-z0-9]{32,}|gm-[A-Za-z0-9]{32,})' \
		-- . ':(exclude)config/*.example.json' ':(exclude)README.md' ':(exclude)docs/**' || true
)"

if [ -n "$secret_matches" ]; then
	warn "potential secret patterns detected:"
	printf "%s\n" "$secret_matches" >&2
else
	echo "security-warn: no obvious secret patterns detected"
fi

echo "security-warn: completed (non-blocking)"
exit 0
