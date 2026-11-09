#!/usr/bin/env bash
# Fails when coderd/apidoc is out of date with the API annotations. swag
# collects enum constants from a package's files in map order, so an enum
# declared across files (codersdk.Experiment and fork_experiments.go) comes
# out in a different order from run to run. The comparison sorts object keys
# and enum arrays so only real drift fails.
set -euo pipefail

canon='walk(if type == "object" then with_entries(if (.key == "enum" or (.key | startswith("x-enum-"))) and (.value | type) == "array" then .value |= sort else . end) else . end)'

# docs.go embeds the spec in a Go raw string, which spells a backtick as
# ` + "`" + `. The only template placeholder outside a JSON string is the
# schemes list.
# shellcheck disable=SC2016 # the backticks are literal Go source.
docs_go_spec() {
	sed -n '/^const docTemplate = `{$/,/^}`$/p' "$1" |
		sed -e '1s/^const docTemplate = `//' -e '$s/`$//' \
			-e 's/` + "`" + `/`/g' -e 's/{{ marshal .Schemes }}/[]/' |
		jq -S "$canon" >"$2"
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/gen"
SWAG_OUTPUT_DIR="$tmp/gen" go run ./scripts/apidocgen/swaginit >/dev/null

docs_go_spec coderd/apidoc/docs.go "$tmp/committed-spec.json"
docs_go_spec "$tmp/gen/docs.go" "$tmp/generated-spec.json"
sed '1,/^}`$/d' coderd/apidoc/docs.go >"$tmp/committed-tail.go"
sed '1,/^}`$/d' "$tmp/gen/docs.go" >"$tmp/generated-tail.go"
jq -S "$canon" coderd/apidoc/swagger.json >"$tmp/committed-swagger.json"
jq -S "$canon" "$tmp/gen/swagger.json" >"$tmp/generated-swagger.json"

status=0
for part in spec.json tail.go swagger.json; do
	diff -u "$tmp/committed-$part" "$tmp/generated-$part" || status=1
done
if [ "$status" -ne 0 ]; then
	echo "coderd/apidoc is out of date. Run make -B coderd/apidoc/.gen and commit the result."
	exit 1
fi
