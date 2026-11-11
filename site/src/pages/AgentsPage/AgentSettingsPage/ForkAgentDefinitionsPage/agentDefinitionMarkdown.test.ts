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

	it("quotes values the server's YAML 1.1 decoder reads as dates", () => {
		const rebuilt = buildAgentDefinitionMarkdown({
			...emptyAgentDefinitionFormValues,
			name: "2026-11-10",
			body: "Review the diff.",
		});
		expect(rebuilt).toBe('---\nname: "2026-11-10"\n---\nReview the diff.\n');
	});

	it("keeps aliases of an owned value when that value changes or is removed", () => {
		const parsed = parseAgentDefinitionMarkdown(
			[
				"---",
				"name: reviewer",
				"description: &summary Reviews diffs.",
				"model: &default sonnet",
				"reasoning_effort: *default",
				"summary: *summary # keep this explanation",
				"---",
				"Review the diff.",
			].join("\n"),
		);
		const rebuilt = buildAgentDefinitionMarkdown({
			...parsed,
			description: "Finds bugs.",
			model: "",
		});
		expect(rebuilt).toBe(
			[
				"---",
				"name: reviewer",
				"description: &summary Finds bugs.",
				"reasoning_effort: sonnet",
				"summary: Reviews diffs. # keep this explanation",
				"---",
				"Review the diff.",
				"",
			].join("\n"),
		);
	});

	it("keeps aliases of owned values that do not change", () => {
		const frontMatter = [
			"---",
			"name: reviewer",
			"description: &summary Reviews diffs.",
			"summary: *summary",
			"---",
		].join("\n");
		const parsed = parseAgentDefinitionMarkdown(
			`${frontMatter}\nReview the diff.`,
		);
		expect(
			buildAgentDefinitionMarkdown({ ...parsed, body: "Find bugs." }),
		).toBe(`${frontMatter}\nFind bugs.\n`);
	});

	it("reads and keeps an owned field set through an alias", () => {
		const content = [
			"---",
			"default_name: &agent_name reviewer",
			"name: *agent_name",
			"description: Reviews diffs.",
			"---",
			"Review the diff.",
			"",
		].join("\n");
		const parsed = parseAgentDefinitionMarkdown(content);
		expect(parsed.name).toBe("reviewer");
		expect(
			buildAgentDefinitionMarkdown({ ...parsed, description: "Finds bugs." }),
		).toBe(content.replace("Reviews diffs.", "Finds bugs."));
	});

	it("quotes values YAML would read as an alias", () => {
		expect(
			buildAgentDefinitionMarkdown({
				...emptyAgentDefinitionFormValues,
				name: "reviewer",
				model: "*sonnet",
				body: "Review the diff.",
			}),
		).toBe('---\nname: reviewer\nmodel: "*sonnet"\n---\nReview the diff.\n');
	});

	it("shows a multiline description on one line and keeps it as written", () => {
		const content = [
			"---",
			"name: reviewer",
			"description: |",
			"  Reviews diffs.",
			"  Finds bugs.",
			"---",
			"Review the diff.",
			"",
		].join("\n");
		const parsed = parseAgentDefinitionMarkdown(content);
		expect(parsed.description).toBe("Reviews diffs. Finds bugs.");
		expect(buildAgentDefinitionMarkdown({ ...parsed, model: "sonnet" })).toBe(
			content.replace("---\nname", "---\nmodel: sonnet\nname"),
		);
	});

	it("reads and keeps an owned field set through a merge key", () => {
		const content = [
			"---",
			"defaults: &agent",
			"  name: reviewer",
			"<<: *agent",
			"description: Reviews diffs.",
			"---",
			"Review the diff.",
			"",
		].join("\n");
		const parsed = parseAgentDefinitionMarkdown(content);
		expect(parsed.name).toBe("reviewer");
		expect(
			buildAgentDefinitionMarkdown({ ...parsed, description: "Finds bugs." }),
		).toBe(content.replace("Reviews diffs.", "Finds bugs."));
	});

	it("keeps the tag and comments of aliases it replaces", () => {
		const parsed = parseAgentDefinitionMarkdown(
			[
				"---",
				"name: reviewer",
				"description: &summary !!str 2026-11-10",
				"default_model: &default sonnet",
				"model: *default # deployment choice",
				"summary: *summary",
				"---",
				"Review the diff.",
			].join("\n"),
		);
		expect(
			buildAgentDefinitionMarkdown({
				...parsed,
				description: "Finds bugs.",
				model: "opus",
			}),
		).toBe(
			[
				"---",
				"name: reviewer",
				"description: &summary !!str Finds bugs.",
				"default_model: &default sonnet",
				"model: opus # deployment choice",
				"summary: !!str 2026-11-10",
				"---",
				"Review the diff.",
				"",
			].join("\n"),
		);
	});

	it("rejects a description over the size limit before it is shown on one line", () => {
		// Each line keeps ten spaces of its own indentation, which the
		// one-line form drops.
		const lines = [
			"  a",
			...Array.from({ length: 400 }, () => `  ${" ".repeat(10)}a`),
		];
		expect(() =>
			parseAgentDefinitionMarkdown(
				["---", "name: reviewer", "description: |", ...lines, "---", ""].join(
					"\n",
				),
			),
		).toThrow("The description must be 4096 bytes or smaller.");
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
