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
});
