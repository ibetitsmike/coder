// This file is fork code: converts agent definition markdown to and from the
// editor's fields.

import frontMatter from "front-matter";

/**
 * The editor's fields. extraFrontMatter holds the raw front-matter lines of
 * the keys the editor does not own, such as tools or mcp.
 */
export type AgentDefinitionFormValues = {
	name: string;
	description: string;
	model: string;
	body: string;
	extraFrontMatter: string;
};

export const emptyAgentDefinitionFormValues: AgentDefinitionFormValues = {
	name: "",
	description: "",
	model: "",
	body: "",
	extraFrontMatter: "",
};

const ownedKeys = new Set(["name", "description", "model"]);

// A line that starts a top-level key, as opposed to a nested value, a list
// item, a comment, or a blank line that continues the previous key.
const topLevelKeyPattern = /^([^\s#-][^:]*):(?:\s|$)/;

const stringField = (attributes: Record<string, unknown>, key: string) => {
	const value = attributes[key];
	if (value === undefined || value === null) {
		return "";
	}
	if (typeof value !== "string") {
		throw new Error(`Front-matter field "${key}" must be a string.`);
	}
	return value.trim();
};

/** Splits agent definition markdown into the editor's fields. */
export const parseAgentDefinitionMarkdown = (
	content: string,
): AgentDefinitionFormValues => {
	const lines = content.replace(/^\uFEFF/, "").split("\n");
	if (lines[0]?.trim() !== "---") {
		throw new Error("The file must start with --- front-matter.");
	}
	const closingIndex = lines.findIndex(
		(line, index) => index > 0 && line.trim() === "---",
	);
	if (closingIndex < 0) {
		throw new Error("The front-matter has no closing ---.");
	}
	const frontMatterLines = lines.slice(1, closingIndex);
	const parsed = frontMatter<Record<string, unknown>>(
		["---", ...frontMatterLines, "---", ...lines.slice(closingIndex + 1)].join(
			"\n",
		),
	);

	const extraLines: string[] = [];
	let keepLine = true;
	for (const line of frontMatterLines) {
		const key = topLevelKeyPattern
			.exec(line)?.[1]
			?.trim()
			.replace(/^(["'])(.*)\1$/, "$2");
		if (key !== undefined) {
			keepLine = !ownedKeys.has(key);
		}
		if (keepLine) {
			extraLines.push(line);
		}
	}

	return {
		name: stringField(parsed.attributes, "name"),
		description: stringField(parsed.attributes, "description"),
		model: stringField(parsed.attributes, "model"),
		body: parsed.body.trim(),
		extraFrontMatter: extraLines.join("\n").trimEnd(),
	};
};

/** Builds agent definition markdown from the editor's fields. */
export const buildAgentDefinitionMarkdown = (
	values: AgentDefinitionFormValues,
): string => {
	// A JSON string is a valid YAML double-quoted scalar, so this escapes
	// quotes, backslashes, and values YAML would read as other types.
	const lines = ["---", `name: ${JSON.stringify(values.name.trim())}`];
	const description = values.description.trim();
	if (description) {
		lines.push(`description: ${JSON.stringify(description)}`);
	}
	const model = values.model.trim();
	if (model) {
		lines.push(`model: ${JSON.stringify(model)}`);
	}
	if (values.extraFrontMatter) {
		lines.push(values.extraFrontMatter);
	}
	lines.push("---");
	return `${lines.join("\n")}\n${values.body.trim()}\n`;
};
