// This file is fork code: the JSON Schema types of the fork-workflows
// experiment. The generated args_schema field is unknown, because the wire
// value is a JSON Schema object whose values are schemas, not strings.

/** A JSON Schema object for the args of a saved workflow run. */
export type ForkSavedWorkflowSchema = {
	type: "object";
	properties?: Record<string, unknown>;
	required?: string[];
} & Record<string, unknown>;
