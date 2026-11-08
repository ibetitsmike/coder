#!/usr/bin/env bash

# Generates the fork queries from queries/*.sql and the mock of Store. Needs
# the sqlc version that upstream's queries.sql.go names. `make gen` does not
# run this script, so run it after you change a query or a fork node.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

rm -f ./*.sql.go db.go models.go querier.go
sqlc generate
go tool golang.org/x/tools/cmd/goimports -w ./*.go
go generate ./forkdbmock
