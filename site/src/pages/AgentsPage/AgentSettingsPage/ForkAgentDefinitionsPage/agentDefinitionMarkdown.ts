// This file is fork code: converts agent definition markdown to and from the
// editor's fields.

import { type Document, isMap, parseDocument } from "yaml";

/**
 * The editor's fields. frontMatter holds the definition's original YAML
 * front-matter, so saving rewrites only the owned keys and keeps every
 * other key and comment.
 */
export type AgentDefinitionFormValues = {
	name: string;
	description: string;
	model: string;
	body: string;
	frontMatter: string;
};

/** The editor's fields for a new agent definition. */
export const emptyAgentDefinitionFormValues: AgentDefinitionFormValues = {
	name: "",
	description: "",
	model: "",
	body: "",
	frontMatter: "",
};

const ownedKeys = ["name", "description", "model"] as const;

const parseFrontMatter = (text: string): Document => {
	const document = parseDocument(text);
	const [error] = document.errors;
	if (error) {
		throw new Error(`The front-matter is not valid YAML: ${error.message}`);
	}
	if (document.contents !== null && !isMap(document.contents)) {
		throw new Error("The front-matter must be a list of key: value fields.");
	}
	return document;
};

const stringField = (document: Document, key: string) => {
	const value = document.get(key);
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
	const frontMatter = lines.slice(1, closingIndex).join("\n");
	const document = parseFrontMatter(frontMatter);
	return {
		name: stringField(document, "name"),
		description: stringField(document, "description"),
		model: stringField(document, "model"),
		body: lines
			.slice(closingIndex + 1)
			.join("\n")
			.trim(),
		frontMatter,
	};
};

/** Builds agent definition markdown from the editor's fields. */
export const buildAgentDefinitionMarkdown = (
	values: AgentDefinitionFormValues,
): string => {
	const document = parseFrontMatter(values.frontMatter);
	for (const key of ownedKeys) {
		const value = values[key].trim();
		if (value) {
			// Setting an existing scalar keeps its node, so its style and
			// comments survive; YAML quotes the value when it needs to.
			document.set(key, value);
		} else if (isMap(document.contents)) {
			document.contents.delete(key);
		}
	}
	// lineWidth 0 keeps long values on one line instead of folding them.
	return `---\n${document.toString({ lineWidth: 0 })}---\n${values.body.trim()}\n`;
};
