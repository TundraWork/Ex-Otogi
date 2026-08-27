#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case ${1-} in
	./testdata/*) fixture="$script_dir/${1#./}" ;;
	*) echo "fixture path must be under ./testdata" >&2; exit 2 ;;
esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp -R "$fixture"/. "$tmp"/
printf 'module qualityfixture\n\ngo 1.27.0\n' > "$tmp/go.mod"
cd "$tmp"
"$script_dir/golangci-lint.sh" run --config "$script_dir/../../.golangci.yml" ./...
