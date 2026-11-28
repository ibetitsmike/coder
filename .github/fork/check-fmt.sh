#!/usr/bin/env bash
# Fails when a non-generated Go file is not formatted the way make fmt/go
# formats it: gofumpt, then gci with the import sections of
# scripts/format_go_file.sh. It only reads files (#407).
set -euo pipefail

# The same files as make fmt/go: FIND_EXCLUSIONS in the Makefile, minus
# files with a "Code generated ... DO NOT EDIT." header.
files=()
while IFS= read -r -d '' f; do
	files+=("$f")
done < <(
	find . \( \( -path '*/.git/*' -o -path './build/*' -o -path './vendor/*' -o -path './.coderv2/*' -o -path '*/node_modules/*' -o -path '*/out/*' -o -path './coderd/apidoc/*' -o -path '*/.next/*' -o -path '*/.terraform/*' -o -path './_gen/*' \) -prune \) -o -type f -name '*.go' -print0 |
		xargs -0 grep -E --null -L '^// Code generated .* DO NOT EDIT\.$'
)
test "${#files[@]}" -gt 0

status=0
unformatted=$(go tool mvdan.cc/gofumpt -l "${files[@]}")
if [ -n "$unformatted" ]; then
	echo "gofumpt would change these files:"
	echo "$unformatted"
	status=1
fi
imports=$(go tool github.com/daixiang0/gci diff -s standard -s default -s "Prefix(github.com/coder,cdr.dev/)" "${files[@]}")
if [ -n "$imports" ]; then
	echo "gci would change these imports:"
	echo "$imports"
	status=1
fi
if [ "$status" -ne 0 ]; then
	echo "Go files are not formatted. Run make fmt/go and commit the result."
fi
exit "$status"
