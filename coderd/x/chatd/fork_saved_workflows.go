package chatd

// This file is fork code: it checks saved workflows of the fork-workflows
// experiment with the same rules run_workflow applies to a script.

import (
	"encoding/json"

	"golang.org/x/xerrors"
)

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
// run_workflow can check args against. Its top level must be an object.
func ValidateWorkflowArgsSchema(raw json.RawMessage) error {
	if _, err := checkStepSchema(raw); err != nil {
		return xerrors.Errorf("args schema: %w", err)
	}
	return nil
}
