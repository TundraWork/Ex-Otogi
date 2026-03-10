#!/usr/bin/env bash
set -euo pipefail

if ! repo_root="$(git rev-parse --show-toplevel 2>/dev/null)"; then
	echo "pre-commit-quality: must run inside a git repository" >&2
	exit 1
fi

cd "$repo_root"

restore_stash_ref=""

cleanup() {
	local status=$?

	if [[ -n "$restore_stash_ref" ]]; then
		if ! git stash pop --index --quiet "$restore_stash_ref"; then
			echo "pre-commit-quality: failed to restore hidden changes from $restore_stash_ref" >&2
			echo "pre-commit-quality: recover them with: git stash pop --index $restore_stash_ref" >&2
			exit 1
		fi
	fi

	exit "$status"
}

trap cleanup EXIT

before_stash_ref="$(git rev-parse -q --verify refs/stash || true)"
stash_message="pre-commit-quality-$$"
git stash push --keep-index --include-untracked --message "$stash_message" --quiet >/dev/null
after_stash_ref="$(git rev-parse -q --verify refs/stash || true)"

if [[ -n "$after_stash_ref" && "$after_stash_ref" != "$before_stash_ref" ]]; then
	restore_stash_ref="stash@{0}"
fi

QUALITY_DIFF_MODE="${QUALITY_DIFF_MODE:-staged}" make quality
