#!/usr/bin/env bash
# Runs the tests in the fork's Go test files (fork_*_test.go), whatever the
# tests are named, so fork-ci needs no name filter or hand-kept package list
# to find them.
#
#   fork-go-tests.sh pattern <dir>  prints a -run pattern matching every test
#                                   in the fork test files directly in <dir>
#   fork-go-tests.sh run            runs those tests in every package with
#                                   fork test files that no other fork-ci step
#                                   already tests
set -euo pipefail

# Other fork-ci steps run these trees in full, run ./codersdk in full
# (agent-sdk), and add the pattern for ./coderd to the "Chat API tests" filter.
covered='^(coderd/x|coderd/database|enterprise/dbcrypt|agent/agentcontext|agent/agentgit)(/|$)|^(coderd|codersdk)$'

pattern() {
	local names
	names=$(git ls-files -z -- ":(glob)$1/fork_*_test.go" |
		xargs -0 sed -nE 's/^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\) \{$/\1/p' |
		sort -u | paste -sd '|' -)
	[[ -n "$names" ]] || {
		echo "no fork tests in $1" >&2
		exit 1
	}
	echo "^(${names})\$"
}

run() {
	local dir run
	for dir in $(git ls-files -- ':(glob)**/fork_*_test.go' | xargs -n1 dirname | sort -u | grep -Ev "$covered"); do
		# A package whose fork test files hold only helpers has nothing to run.
		run=$(pattern "$dir" 2>/dev/null) || continue
		echo "==> ./$dir"
		go test -count=1 -run "$run" "./$dir"
	done
}

case "${1:-} $#" in
"pattern 2") pattern "$2" ;;
"run 1") run ;;
*)
	echo "usage: $0 {pattern <dir>|run}" >&2
	exit 2
	;;
esac
