import { describe, expect, it } from "vitest";
import {
	buildAgentDefinitionMarkdown,
	emptyAgentDefinitionFormValues,
	parseAgentDefinitionMarkdown,
} from "./agentDefinitionMarkdown";

describe("agent definition markdown", () => {
	it("rewrites the owned fields and keeps every other key and comment", () => {
		const content = [
			"---",
			"name: code-reviewer # immutable identifier",
			"description: >",
			"  Reviews diffs",
			"  for bugs.",
			"# The tools it may use.",
			"tools:",
			"  - Read",
			"  - Grep",
			"model: sonnet",
			"readonly: true",
			"---",
			"",
			"Review the diff.",
			"",
		].join("\n");

		const parsed = parseAgentDefinitionMarkdown(content);
		expect(parsed).toMatchObject({
			name: "code-reviewer",
			description: "Reviews diffs for bugs.",
			model: "sonnet",
			body: "Review the diff.",
		});

		const rebuilt = buildAgentDefinitionMarkdown({
			...parsed,
			description: 'Says "no" to: bugs',
			model: "",
		});
		expect(rebuilt).toBe(
			[
				"---",
				"name: code-reviewer # immutable identifier",
				'description: >-\n  Says "no" to: bugs',
				"# The tools it may use.",
				"tools:",
				"  - Read",
				"  - Grep",
				"readonly: true",
				"---",
				"Review the diff.",
				"",
			].join("\n"),
		);
	});

	it("rebuilds uniformly indented front-matter as valid YAML", () => {
		const content = [
			"---",
			"  name: reviewer",
			"  tools: Read",
			"---",
			"Review the diff.",
		].join("\n");

		const parsed = parseAgentDefinitionMarkdown(content);
		const rebuilt = buildAgentDefinitionMarkdown({
			...parsed,
			name: "code-reviewer",
		});
		expect(rebuilt).toBe(
			"---\nname: code-reviewer\ntools: Read\n---\nReview the diff.\n",
		);
	});

	it("puts owned keys the file lacks before its other keys", () => {
		const parsed = parseAgentDefinitionMarkdown(
			"---\nmodel: sonnet\ntools: Read\n---\nWrite release notes.",
		);
		const rebuilt = buildAgentDefinitionMarkdown({
			...parsed,
			name: "release-notes",
			description: "Writes release notes.",
		});
		expect(rebuilt).toBe(
			[
				"---",
				"name: release-notes",
				"description: Writes release notes.",
				"model: sonnet",
				"tools: Read",
				"---",
				"Write release notes.",
				"",
			].join("\n"),
		);
	});

	it("quotes values YAML would read as another type", () => {
		const rebuilt = buildAgentDefinitionMarkdown({
			...emptyAgentDefinitionFormValues,
			name: "reviewer",
			description: "true",
			body: "Review the diff.",
		});
		expect(rebuilt).toBe(
			'---\nname: reviewer\ndescription: "true"\n---\nReview the diff.\n',
		);
		expect(parseAgentDefinitionMarkdown(rebuilt).description).toBe("true");
	});

	it("rejects front-matter that is not a YAML mapping", () => {
		expect(() =>
			parseAgentDefinitionMarkdown("---\nname: [reviewer\n---\nbody"),
		).toThrow(/not valid YAML/);
		expect(() =>
			parseAgentDefinitionMarkdown("---\n- reviewer\n---\nbody"),
		).toThrow(/key: value/);
	});
});
