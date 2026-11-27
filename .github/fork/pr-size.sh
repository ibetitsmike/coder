#!/usr/bin/env bash
# Reports the size of a pull request: additions plus deletions of its changed
# files from the merge base with the base branch, without generated files.
# Fork PRs should stay at or under 500 such lines (#171). The lead's merge
# step enforces the limit with the same generated-file list; this script only
# reports and never fails the check.
#
#   pr-size.sh <base-sha> <head-sha>
#
# Writes to $GITHUB_STEP_SUMMARY when it is set and emits a ::warning:: over
# the limit. A sync PR, one that merges new upstream coder/coder commits or
# runs with FORK_PR_SYNC=true, is reported without the limit. The upstream
# check does not rely on the sync label, which gh adds after the opened
# event.
set -euo pipefail

limit=500
generated='(\.sql\.go$|forkdbmock/|^coderd/apidoc/|^docs/reference/|typesGenerated\.ts$|Generated\.|\.pb\.go$|_gen\.go$|dbmock/|(^|/)querier\.go$|(^|/)models\.go$|\.golden$|pnpm-lock\.yaml$|dump\.sql$|^docs/manifest\.json$|^docs/admin/security/audit-logs\.md$)'

if [[ $# -ne 2 ]]; then
	echo "usage: $0 <base-sha> <head-sha>" >&2
	exit 2
fi

base=$(git merge-base "$1" "$2")
head=$2
summary=${GITHUB_STEP_SUMMARY:-/dev/stdout}

# A sync PR brings upstream commits that the base does not have yet. A PR
# branch that is behind the base has an older upstream merge base, which the
# base already contains.
sync=${FORK_PR_SYNC:-false}
if [[ "$sync" != true ]] &&
	git fetch --quiet --no-tags --filter=blob:none https://github.com/coder/coder.git main 2>/dev/null; then
	upstream_base=$(git merge-base "$head" FETCH_HEAD || true)
	if [[ -n "$upstream_base" ]] && ! git merge-base --is-ancestor "$upstream_base" "$base"; then
		sync=true
	fi
fi

# Write the diff to a file first so that a failing git diff, for example a
# blob the blobless clone cannot fetch, fails the script instead of
# reporting a partial count.
numstat=$(mktemp)
trap 'rm -f "$numstat"' EXIT
git diff -z --numstat -M "$base" "$head" >"$numstat"

total=0
skipped=0
files=0
rows=()
# -z keeps paths unquoted. A rename has an empty path followed by the old
# and the new path; the new path decides whether it is generated.
while IFS=$'\t' read -r -d '' add del path; do
	if [[ -z "$path" ]]; then
		IFS= read -r -d '' _
		IFS= read -r -d '' path
	fi
	# Binary files show - for both counts.
	[[ "$add" == - ]] && add=0
	[[ "$del" == - ]] && del=0
	n=$((add + del))
	if [[ "$path" =~ $generated ]]; then
		skipped=$((skipped + n))
		continue
	fi
	total=$((total + n))
	files=$((files + 1))
	rows+=("$(printf '%d\t%s' "$n" "$path")")
done <"$numstat"

{
	echo "## PR size"
	echo
	echo "**${total}** non-generated changed lines in ${files} files (limit ${limit}), merge base \`${base:0:10}\`. Generated files add ${skipped} more lines."
	if [[ "$sync" == true ]]; then
		echo
		echo "This PR merges upstream commits, so the limit does not apply."
	elif ((total > limit)); then
		echo
		echo "Over the limit. The merge needs a stated reason."
	fi
	if ((files > 0)); then
		echo
		echo "<details><summary>Counted files</summary>"
		echo
		echo "| Lines | File |"
		echo "|---:|---|"
		printf '%s\n' "${rows[@]}" | sort -t$'\t' -k1,1nr -k2 | awk -F'\t' '{ printf "| %d | `%s` |\n", $1, $2 }'
		echo
		echo "</details>"
	fi
} >>"$summary"

echo "non-generated changed lines: $total (generated: $skipped)"
if [[ "$sync" != true ]] && ((total > limit)); then
	echo "::warning title=PR size::${total} non-generated changed lines, over the ${limit} line limit. The merge needs a stated reason."
fi
