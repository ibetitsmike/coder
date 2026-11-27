#!/usr/bin/env bash
# Finds the fork's tests so fork-ci runs them without a name filter or a
# hand-kept package list. A fork test file is a test file that the fork added
# or changed: it differs between the merge base with upstream coder/coder main
# and HEAD. Every top-level test in such a file is a fork test, whatever its
# name, and a new fork package or site directory is picked up on its own.
#
#   fork-tests.sh files               print the fork's Go and site test files
#   fork-tests.sh go-pattern <dir>... print a -run pattern that matches every
#                                     test in the fork's Go test files directly
#                                     in the given directories
#   fork-tests.sh go-run              run the fork's Go tests in every package
#                                     that the other fork-ci steps do not cover
#   fork-tests.sh site-files          print the fork's site test files outside
#                                     src/pages/AgentsPage, relative to site/
#
# It needs the full history (actions/checkout with fetch-depth: 0) and fetches
# upstream main, unless FORK_UPSTREAM_BASE names the merge base.
set -euo pipefail

upstream_url=https://github.com/coder/coder.git

# Packages that other fork-ci steps test in full, or, for ./coderd and
# ./enterprise/coderd, with the go-pattern of their fork tests. Keep this in
# sync with .github/workflows/fork-ci.yaml.
covered='^(coderd/x|coderd/database|enterprise/dbcrypt|agent/agentcontext|agent/agentgit|agent/x/agentmcp)(/|$)|^(coderd|enterprise/coderd|codersdk)$'

cd "$(git rev-parse --show-toplevel)"

base() {
	if [[ -n "${FORK_UPSTREAM_BASE:-}" ]]; then
		echo "$FORK_UPSTREAM_BASE"
		return
	fi
	git fetch --quiet --no-tags --filter=blob:none "$upstream_url" main
	git merge-base HEAD FETCH_HEAD
}

# --no-renames keeps git from reading blobs, which a blobless clone would
# fetch one by one. A renamed file shows up as added.
files() {
	local b
	b=$(base)
	git diff --name-only --no-renames --diff-filter=AM "$b" HEAD -- \
		'*_test.go' 'site/src/*.test.ts' 'site/src/*.test.tsx' |
		grep -v '/testdata/' || true
}

go_files() {
	files | grep '_test\.go$' || true
}

# Prints the names of the top-level tests in the files on stdin.
test_names() {
	local f
	while IFS= read -r f; do
		sed -nE 's/^func (Test[A-Za-z0-9_]*)\([A-Za-z_][A-Za-z0-9_]* \*testing\.T\) \{$/\1/p' "$f"
	done | sort -u
}

# Prints ^(A|B|...)$ for the tests in the files on stdin, or fails if there
# are none. An empty pattern would match every test.
pattern_of() {
	local names
	names=$(test_names | paste -sd '|' -)
	[[ -n "$names" ]] || return 1
	echo "^(${names})\$"
}

go_pattern() {
	local all dir
	all=$(go_files)
	for dir in "$@"; do
		awk -v d="${dir%/}" '{ p = $0; sub(/\/[^\/]*$/, "", p); if (p == d) print }' <<<"$all"
	done | pattern_of || {
		echo "no fork tests in $*" >&2
		exit 1
	}
}

# One go test call builds and runs the packages in parallel. The pattern is
# the union of the packages' fork tests, so a package also runs an upstream
# test that shares a name with a fork test elsewhere, which is harmless.
go_run() {
	local all dirs run f d
	all=$(go_files | while IFS= read -r f; do
		d=$(dirname "$f")
		grep -Eq "$covered" <<<"$d" || echo "$f"
	done)
	if [[ -z "$all" ]]; then
		echo "no fork tests outside the covered packages"
		return
	fi
	mapfile -t dirs < <(xargs -n1 dirname <<<"$all" | sort -u | sed 's|^|./|')
	run=$(pattern_of <<<"$all") || {
		echo "the fork test files outside the covered packages hold no tests"
		return
	}
	echo "packages: ${dirs[*]}"
	echo "-run $run"
	go test -count=1 -run "$run" "${dirs[@]}"
}

site_files() {
	files | grep -E '^site/src/.*\.test\.tsx?$' | grep -v '^site/src/pages/AgentsPage/' | sed 's|^site/||' || true
}

case "${1:-}" in
files) files ;;
go-pattern)
	shift
	[[ $# -gt 0 ]] || {
		echo "usage: $0 go-pattern <dir>..." >&2
		exit 2
	}
	go_pattern "$@"
	;;
go-run) go_run ;;
site-files) site_files ;;
*)
	echo "usage: $0 {files|go-pattern <dir>...|go-run|site-files}" >&2
	exit 2
	;;
esac
