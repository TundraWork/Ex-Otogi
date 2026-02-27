#!/usr/bin/env bash
set -euo pipefail

mode="${QUALITY_DIFF_MODE:-working-tree}"
exception_file="${TEST_EXCEPTION_FILE:-config/quality/test_required_exceptions.txt}"

if [[ "$mode" != "staged" && "$mode" != "working-tree" ]]; then
	echo "agent-guard: unsupported QUALITY_DIFF_MODE='$mode' (expected staged|working-tree)" >&2
	exit 1
fi

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	echo "agent-guard: must run inside a git repository" >&2
	exit 1
fi

if [[ "$mode" == "staged" ]]; then
	readonly DIFF_NAME_ARGS=(--cached)
	readonly DIFF_PATCH_ARGS=(--cached --unified=0)
else
	if git rev-parse --verify HEAD >/dev/null 2>&1; then
		readonly DIFF_NAME_ARGS=(HEAD)
		readonly DIFF_PATCH_ARGS=(HEAD --unified=0)
	else
		readonly DIFF_NAME_ARGS=(--cached)
		readonly DIFF_PATCH_ARGS=(--cached --unified=0)
	fi
fi

mapfile -t changed_go_files < <(git diff --name-only --diff-filter=ACMR "${DIFF_NAME_ARGS[@]}" -- '*.go')
untracked_go_files=()
if [[ "$mode" == "working-tree" ]]; then
	mapfile -t untracked_go_files < <(git ls-files --others --exclude-standard -- '*.go')
	changed_go_files+=("${untracked_go_files[@]}")
fi

if (( ${#changed_go_files[@]} > 0 )); then
	declare -A seen_paths=()
	deduped_changed_files=()
	for file in "${changed_go_files[@]}"; do
		if [[ -z "$file" ]]; then
			continue
		fi
		if [[ -n "${seen_paths[$file]:-}" ]]; then
			continue
		fi
		seen_paths["$file"]=1
		deduped_changed_files+=("$file")
	done
	changed_go_files=("${deduped_changed_files[@]}")
fi

if (( ${#changed_go_files[@]} == 0 )); then
	echo "agent-guard: no changed Go files to inspect"
	exit 0
fi

patch="$(git diff "${DIFF_PATCH_ARGS[@]}" -- '*.go' || true)"
if (( ${#untracked_go_files[@]} > 0 )); then
	for file in "${untracked_go_files[@]}"; do
		patch+=$'\n'"$(git diff --no-index -- /dev/null "$file" || true)"
	done
fi

panic_hits="$(
	printf "%s\n" "$patch" | awk '
		/^\+\+\+ b\// { file = substr($0, 7); next }
		/^\+[^+]/ {
			if (file ~ /_test\.go$/) next
			if ($0 ~ /(^|[^[:alnum:]_])panic[[:space:]]*\(/) {
				line = $0
				sub(/^\+/, "", line)
				printf "%s: %s\n", file, line
			}
		}
	'
)"
if [[ -n "$panic_hits" ]]; then
	echo "agent-guard: added panic(...) is forbidden outside tests:" >&2
	printf "%s\n" "$panic_hits" >&2
	exit 1
fi

todo_hits="$(
	printf "%s\n" "$patch" | awk '
		/^\+\+\+ b\// { file = substr($0, 7); next }
		/^\+[^+]/ {
			if (file ~ /_test\.go$/) next
			if ($0 ~ /(TODO|FIXME)/) {
				line = $0
				sub(/^\+/, "", line)
				printf "%s: %s\n", file, line
			}
		}
	'
)"
if [[ -n "$todo_hits" ]]; then
	echo "agent-guard: added TODO/FIXME is forbidden outside tests:" >&2
	printf "%s\n" "$todo_hits" >&2
	exit 1
fi

exception_patterns=()
if [[ -f "$exception_file" ]]; then
	while IFS= read -r raw_line; do
		line="${raw_line%%#*}"
		line="${line#"${line%%[![:space:]]*}"}"
		line="${line%"${line##*[![:space:]]}"}"
		if [[ -n "$line" ]]; then
			exception_patterns+=("$line")
		fi
	done < "$exception_file"
fi

matches_exception() {
	local file="$1"
	local pattern
	for pattern in "${exception_patterns[@]}"; do
		if [[ "$file" == $pattern ]]; then
			return 0
		fi
	done
	return 1
}

prod_changed=()
test_changed=()
for file in "${changed_go_files[@]}"; do
	if [[ "$file" == *_test.go ]]; then
		test_changed+=("$file")
	else
		prod_changed+=("$file")
	fi
done

non_exempt_prod=()
for file in "${prod_changed[@]}"; do
	if ! matches_exception "$file"; then
		non_exempt_prod+=("$file")
	fi
done

if (( ${#non_exempt_prod[@]} > 0 && ${#test_changed[@]} == 0 )); then
	echo "agent-guard: production Go files changed without any test updates" >&2
	echo "agent-guard: changed production files requiring tests:" >&2
	printf "  - %s\n" "${non_exempt_prod[@]}" >&2
	echo "agent-guard: add/update *_test.go files or whitelist paths in $exception_file" >&2
	exit 1
fi

echo "agent-guard: policy checks passed"
