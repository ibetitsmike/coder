import { describe, expect, it } from "vitest";
import { MockForkWorkflowScript } from "#/testHelpers/forkWorkflows";
import {
	buildCreateSavedWorkflowRequest,
	buildUpdateSavedWorkflowRequest,
	savedWorkflowArgsSchemaSizeBytes,
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

	it("rejects numbers that JSON.parse would round", () => {
		for (const argsSchema of [
			'{"maximum": 9007199254740993}',
			'{"minimum": -9007199254740993}',
			'{"maximum": 1e400}',
			'{"minimum": 1e-400}',
		]) {
			expect(
				savedWorkflowFormErrors({ ...validValues, argsSchema }).argsSchema,
			).toMatch(/numbers must stay exact/);
		}
	});

	it("accepts safe numbers and large numbers inside strings", () => {
		expect(
			savedWorkflowFormErrors({
				...validValues,
				argsSchema:
					'{"maximum": 9007199254740991, "minimum": -1.5e3, "multipleOf": 0e-400, "default": "9007199254740993 \\" 1e400"}',
			}),
		).toEqual({});
	});

	it("measures the args schema size without whitespace", () => {
		const property = '"p": {"type": "string"}';
		const compact = `{${Array.from({ length: 300 }, (_, i) => property.replace("p", `p${i}`)).join(",")}}`;
		const spaced = JSON.stringify(JSON.parse(compact), null, 8);
		expect(savedWorkflowArgsSchemaSizeBytes(spaced)).toBeLessThan(12 << 10);
		expect(spaced.length).toBeGreaterThan(12 << 10);
		expect(
			savedWorkflowFormErrors({ ...validValues, argsSchema: spaced })
				.argsSchema,
		).toBeUndefined();
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
