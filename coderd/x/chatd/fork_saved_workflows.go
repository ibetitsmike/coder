package chatd

// This file is fork code: it checks saved workflows of the fork-workflows
// experiment with the same rules run_workflow applies to a script.

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"golang.org/x/xerrors"
)

// maxWorkflowArgsSchemaDepth bounds the nesting of a saved args schema.
// PostgreSQL's recursive jsonb parser fails on deep input that Go accepts.
const maxWorkflowArgsSchemaDepth = 64

// ValidateWorkflowScript reports whether script compiles as a workflow:
// it fits the run_workflow size limit, parses, names only the host API and
// defines main(args).
func ValidateWorkflowScript(script string) error {
	if len(script) > maxScriptBytes {
		return xerrors.Errorf("the script is %d bytes; at most %d bytes are allowed", len(script), maxScriptBytes)
	}
	_, err := compileWorkflow(script)
	return err
}

// ValidateWorkflowArgsSchema reports whether raw is a JSON Schema that
// run_workflow can check args against. Its top level must be an object,
// its nesting at most maxWorkflowArgsSchemaDepth, and its numbers in the
// range args are validated in.
func ValidateWorkflowArgsSchema(raw json.RawMessage) error {
	if err := checkJSONDepth(raw, maxWorkflowArgsSchemaDepth); err != nil {
		return xerrors.Errorf("args schema: %w", err)
	}
	if _, err := checkStepSchema(raw); err != nil {
		return xerrors.Errorf("args schema: %w", err)
	}
	if err := rejectOutOfRangeNumbers(string(raw)); err != nil {
		return xerrors.Errorf("args schema: %w", err)
	}
	return nil
}

// checkJSONDepth fails when raw nests objects and arrays deeper than limit.
func checkJSONDepth(raw json.RawMessage, limit int) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	depth := 0
	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return xerrors.Errorf("decode: %w", err)
		}
		delim, ok := token.(json.Delim)
		if !ok {
			continue
		}
		switch delim {
		case '{', '[':
			depth++
			if depth > limit {
				return xerrors.Errorf("the JSON nests deeper than %d levels", limit)
			}
		default:
			depth--
		}
	}
}
