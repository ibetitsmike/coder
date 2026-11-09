package main

// This file is fork code: fork nodes add api_key_scope values that upstream's
// dump.sql does not have. See
// coderd/database/migrations/chatdmig/README.md#fork-queries.

import (
	"os"
	"path/filepath"
	"regexp"

	"golang.org/x/xerrors"
)

var forkScopeRe = regexp.MustCompile(`(?i)ALTER TYPE (?:public\.)?api_key_scope ADD VALUE (?:IF NOT EXISTS )?'([^']+)'`)

// addForkEnumValues adds the api_key_scope values of the fork nodes next to
// the dump at dumpPath to values.
func addForkEnumValues(dumpPath string, values map[string]struct{}) error {
	files, err := filepath.Glob(filepath.Join(filepath.Dir(dumpPath), "migrations", "chatdmig", "nodes", "*.up.sql"))
	if err != nil {
		return xerrors.Errorf("list fork nodes: %w", err)
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			return xerrors.Errorf("read fork node: %w", err)
		}
		for _, m := range forkScopeRe.FindAllSubmatch(body, -1) {
			values[string(m[1])] = struct{}{}
		}
	}
	return nil
}
