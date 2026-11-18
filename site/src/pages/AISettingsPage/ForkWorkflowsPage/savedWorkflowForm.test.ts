import { describe, expect, it } from "vitest";
import { MockForkWorkflowScript } from "#/testHelpers/forkWorkflows";
import {
	buildCreateSavedWorkflowRequest,
	buildUpdateSavedWorkflowRequest,
	savedWorkflowFormErrors,
} from "./savedWorkflowForm";

const validValues = {
	name: "review-files",
	description: "Reviews files for bugs.",
	script: MockForkWorkflowScript,
	argsSchema: '{"type": "object"}',
	phases: "review\nsummarize",
};

describe("savedWorkflowFormErrors", () => {
	it("accepts valid values", () => {
		expect(savedWorkflowFormErrors(validValues)).toEqual({});
	});

	it("requires a name in the saved workflow name format", () => {
		expect(
			savedWorkflowFormErrors({ ...validValues, name: "" }).name,
		).toBeDefined();
		expect(
			savedWorkflowFormErrors({ ...validValues, name: "Review Files" }).name,
		).toBeDefined();
	});

	it("rejects a script without main(args)", () => {
		expect(
			savedWorkflowFormErrors({ ...validValues, script: "log('hi')" }).script,
		).toBeDefined();
	});

	it("rejects args schema that is not a JSON object", () => {
		expect(
			savedWorkflowFormErrors({ ...validValues, argsSchema: "[" }).argsSchema,
		).toBeDefined();
		expect(
			savedWorkflowFormErrors({ ...validValues, argsSchema: "[1]" }).argsSchema,
		).toBeDefined();
	});
});

describe("buildUpdateSavedWorkflowRequest", () => {
	it("sends null for a cleared args schema, which removes it", () => {
		expect(
			buildUpdateSavedWorkflowRequest({ ...validValues, argsSchema: "  " })
				.args_schema,
		).toBeNull();
	});

	it("keeps the parsed args schema", () => {
		expect(buildUpdateSavedWorkflowRequest(validValues).args_schema).toEqual({
			type: "object",
		});
	});
});

describe("buildCreateSavedWorkflowRequest", () => {
	it("leaves cleared optional fields out of the request", () => {
		const req = buildCreateSavedWorkflowRequest({
			...validValues,
			argsSchema: "",
			phases: "",
		});
		expect(req.args_schema).toBeUndefined();
		expect(req.phases).toBeUndefined();
	});
});
