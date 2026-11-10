import { describe, expect, it } from "vitest";
import {
	buildAgentDefinitionMarkdown,
	parseAgentDefinitionMarkdown,
} from "./agentDefinitionMarkdown";

describe("agent definition markdown", () => {
	it("rewrites the owned fields and keeps every other key byte for byte", () => {
		const unownedLines = [
			"# The tools it may use.",
			"tools:",
			"- Read",
			"- Grep",
			"# Only the GitHub server.",
			"mcpServers:",
			"  - github",
			"readonly: true",
		];
		const content = [
			"---",
			"name: code-reviewer",
			"description: >",
			"  Reviews diffs",
			"  for bugs.",
			...unownedLines,
			"model: sonnet",
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

		const edited = { ...parsed, description: 'Says "no" to: bugs' };
		const rebuilt = buildAgentDefinitionMarkdown(edited);
		expect(rebuilt).toBe(
			[
				"---",
				'name: "code-reviewer"',
				'description: "Says \\"no\\" to: bugs"',
				'model: "sonnet"',
				...unownedLines,
				"---",
				"Review the diff.",
				"",
			].join("\n"),
		);
		expect(parseAgentDefinitionMarkdown(rebuilt)).toEqual(edited);
	});

	it("keeps indented comments after owned keys but not the text of their values", () => {
		const content = [
			"---",
			"name: code-reviewer",
			"  # After a plain value.",
			"description: |",
			"  Reviews diffs.",
			"  # Part of the description.",
			'model: "son',
			'  # part of the model."',
			"  # After a closed quote.",
			"tools: Read",
			"---",
			"Review the diff.",
		].join("\n");

		expect(parseAgentDefinitionMarkdown(content)).toEqual({
			name: "code-reviewer",
			description: "Reviews diffs.\n# Part of the description.",
			model: "son # part of the model.",
			body: "Review the diff.",
			extraFrontMatter: [
				"  # After a plain value.",
				"  # After a closed quote.",
				"tools: Read",
			].join("\n"),
		});
	});
});
