// This file is fork code: form values and validation for the saved workflow
// editor (fork-workflows experiment).

import type { ForkSavedWorkflowSchema } from "#/api/forkSavedWorkflowSchema";
import type { UpdateSavedWorkflowRequest } from "#/api/forkSavedWorkflows";
import type { CreateForkSavedWorkflowRequest } from "#/api/typesGenerated";
import { getSkillContentSizeBytes } from "#/pages/AgentsPage/utils/skills";

// The backend limits, from coderd/fork_saved_workflows.go, except the
// script limit, from coderd/x/chatd/fork_workflow.go.
const SAVED_WORKFLOW_MAX_NAME_BYTES = 128;
const SAVED_WORKFLOW_MAX_DESCRIPTION_BYTES = 4096;
export const SAVED_WORKFLOW_MAX_SCRIPT_BYTES = 65536;
export const SAVED_WORKFLOW_MAX_ARGS_SCHEMA_BYTES = 12 << 10;
const SAVED_WORKFLOW_MAX_PHASES = 32;
const SAVED_WORKFLOW_MAX_PHASE_BYTES = 128;
/** The most saved workflows one user or one organization can hold. */
export const SAVED_WORKFLOW_MAX_PER_SCOPE = 100;

// Matches the saved_workflows_name_format constraint.
const savedWorkflowNamePattern = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/** Whether a name matches the saved workflow name format and byte limit. */
const isValidSavedWorkflowName = (name: string): boolean =>
	savedWorkflowNamePattern.test(name) &&
	getSkillContentSizeBytes(name) <= SAVED_WORKFLOW_MAX_NAME_BYTES;

/** The fields the saved workflow editor edits. */
export type SavedWorkflowFormValues = {
	name: string;
	description: string;
	script: string;
	argsSchema: string;
	phases: string;
};

/** The values a create dialog starts from. */
export const emptySavedWorkflowFormValues: SavedWorkflowFormValues = {
	name: "",
	description: "",
	script: "",
	argsSchema: "",
	phases: "",
};

/**
 * The phases field holds the names one per line, in order, which a workflow
 * script declares across its waits.
 */
const parseSavedWorkflowPhases = (value: string): string[] =>
	value
		.split("\n")
		.map((phase) => phase.trim())
		.filter((phase) => phase !== "");

/** The phases of a workflow as the editor edits them, one per line. */
export const formatSavedWorkflowPhases = (phases: readonly string[]): string =>
	phases.join("\n");

const toArgsSchemaObject = (argsSchema: string) => {
	if (argsSchema.trim() === "") {
		return undefined;
	}
	const parsed: unknown = JSON.parse(argsSchema);
	return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)
		? (parsed as ForkSavedWorkflowSchema)
		: undefined;
};

// The backend compiles the script and reports a script that does not
// define main(args) as invalid, so the form checks for that here.
const savedWorkflowScriptError = (script: string): string | undefined => {
	if (script.trim() === "" || !/^def\s+main\s*\(/m.test(script)) {
		return "Script must define main(args), as a run_workflow script does.";
	}
	// The request sends the script without trailing whitespace.
	if (
		getSkillContentSizeBytes(script.trimEnd()) > SAVED_WORKFLOW_MAX_SCRIPT_BYTES
	) {
		return `Script must be ${SAVED_WORKFLOW_MAX_SCRIPT_BYTES} bytes or smaller.`;
	}
	return undefined;
};

// Matches JSON strings, so their contents are skipped, and JSON numbers.
const jsonStringOrNumberPattern =
	/"(?:[^"\\]|\\.)*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/g;

/**
 * Whether valid JSON text holds a number that JSON.parse cannot keep
 * exactly: an integer beyond Number.MAX_SAFE_INTEGER, a value too large,
 * or a nonzero value so small it becomes 0. The browser would send a
 * rounded value, which is not what the user wrote.
 */
const hasUnsafeJSONNumber = (json: string): boolean => {
	for (const [token] of json.matchAll(jsonStringOrNumberPattern)) {
		if (token.startsWith('"')) {
			continue;
		}
		const value = Number(token);
		if (
			!Number.isFinite(value) ||
			(Number.isInteger(value) && !Number.isSafeInteger(value)) ||
			(value === 0 && /[1-9]/.test(token.split(/[eE]/)[0]))
		) {
			return true;
		}
	}
	return false;
};

/**
 * The size the server counts for an args schema: its compact form, so
 * whitespace does not count. Text that is not valid JSON counts as is.
 */
export const savedWorkflowArgsSchemaSizeBytes = (
	argsSchema: string,
): number => {
	try {
		return getSkillContentSizeBytes(JSON.stringify(JSON.parse(argsSchema)));
	} catch {
		return getSkillContentSizeBytes(argsSchema);
	}
};

type SavedWorkflowFormErrors = Partial<
	Record<keyof SavedWorkflowFormValues, string>
>;

/** Validates the editor's form values against the server's field limits. */
export const savedWorkflowFormErrors = (
	values: SavedWorkflowFormValues,
): SavedWorkflowFormErrors => {
	const errors: SavedWorkflowFormErrors = {};
	const name = values.name.trim();
	if (name === "") {
		errors.name = "Name is required.";
	} else if (!isValidSavedWorkflowName(name)) {
		errors.name = `Use lowercase letters, numbers, and hyphens, at most ${SAVED_WORKFLOW_MAX_NAME_BYTES} bytes.`;
	}
	if (
		// The request sends the trimmed description.
		getSkillContentSizeBytes(values.description.trim()) >
		SAVED_WORKFLOW_MAX_DESCRIPTION_BYTES
	) {
		errors.description = `Description must be ${SAVED_WORKFLOW_MAX_DESCRIPTION_BYTES} bytes or smaller.`;
	}
	const scriptError = savedWorkflowScriptError(values.script);
	if (scriptError) {
		errors.script = scriptError;
	}
	const argsSchema = values.argsSchema.trim();
	if (argsSchema !== "") {
		let parsed: unknown;
		try {
			parsed = JSON.parse(argsSchema);
		} catch {
			parsed = undefined;
		}
		if (parsed === undefined) {
			errors.argsSchema = "Args schema must be valid JSON.";
		} else if (
			typeof parsed !== "object" ||
			parsed === null ||
			Array.isArray(parsed)
		) {
			errors.argsSchema = "Args schema must be a JSON object.";
		} else if (hasUnsafeJSONNumber(argsSchema)) {
			errors.argsSchema = `Args schema numbers must stay exact in the browser: integers between -${Number.MAX_SAFE_INTEGER} and ${Number.MAX_SAFE_INTEGER}, and no values too large or too small to store. Write such values as strings.`;
		} else if (
			savedWorkflowArgsSchemaSizeBytes(argsSchema) >
			SAVED_WORKFLOW_MAX_ARGS_SCHEMA_BYTES
		) {
			errors.argsSchema = `Args schema must be ${SAVED_WORKFLOW_MAX_ARGS_SCHEMA_BYTES} bytes or smaller without whitespace.`;
		}
	}
	const phases = parseSavedWorkflowPhases(values.phases);
	if (phases.length > SAVED_WORKFLOW_MAX_PHASES) {
		errors.phases = `A workflow can declare at most ${SAVED_WORKFLOW_MAX_PHASES} phases.`;
	} else {
		for (const phase of phases) {
			if (
				getSkillContentSizeBytes(phase) > SAVED_WORKFLOW_MAX_PHASE_BYTES ||
				/[\p{Cc}\p{Cf}]/u.test(phase)
			) {
				errors.phases = `Each phase must be text without control or format characters, at most ${SAVED_WORKFLOW_MAX_PHASE_BYTES} bytes.`;
				break;
			}
		}
	}
	return errors;
};

/**
 * Builds a create request from the form values. Empty optional fields are
 * left out, so the server stores no schema and no phases.
 */
export const buildCreateSavedWorkflowRequest = (
	values: SavedWorkflowFormValues,
): CreateForkSavedWorkflowRequest => {
	const argsSchema = toArgsSchemaObject(values.argsSchema);
	const phases = parseSavedWorkflowPhases(values.phases);
	return {
		name: values.name.trim(),
		description: values.description.trim(),
		script: values.script.trimEnd(),
		...(argsSchema ? { args_schema: argsSchema } : {}),
		...(phases.length > 0 ? { phases } : {}),
	};
};

/**
 * Builds an update request from the form values. The update always sends the
 * script, the description, and the phases, so clearing a field clears it on
 * the server. A cleared args schema sends null, which removes the schema,
 * while an absent one would leave it unchanged.
 */
export const buildUpdateSavedWorkflowRequest = (
	values: SavedWorkflowFormValues,
): UpdateSavedWorkflowRequest => {
	const argsSchema = toArgsSchemaObject(values.argsSchema);
	return {
		description: values.description.trim(),
		script: values.script.trimEnd(),
		args_schema: argsSchema ?? null,
		phases: parseSavedWorkflowPhases(values.phases),
	};
};
