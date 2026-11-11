// This file is fork code: converts agent definition markdown to and from the
// editor's fields.

import {
	type Document,
	isAlias,
	isMap,
	isScalar,
	type Node,
	parseDocument,
	Scalar,
	visit,
} from "yaml";
import { isValidSkillDescription } from "../../utils/skills";

/**
 * frontMatter holds the definition's original YAML front-matter, so saving
 * rewrites only the owned keys and keeps every other key and comment.
 */
export type AgentDefinitionFormValues = {
	name: string;
	description: string;
	model: string;
	body: string;
	frontMatter: string;
};

/** An empty model means the definition uses the chat's model. */
export const emptyAgentDefinitionFormValues: AgentDefinitionFormValues = {
	name: "",
	description: "",
	model: "",
	body: "",
	frontMatter: "",
};

const ownedKeys = ["name", "description", "model"] as const;

const parseFrontMatter = (text: string): Document => {
	// The server's decoder applies << merge keys.
	const document = parseDocument(text, { merge: true });
	const [error] = document.errors;
	if (error) {
		throw new Error(`The front-matter is not valid YAML: ${error.message}`);
	}
	if (document.contents !== null && !isMap(document.contents)) {
		throw new Error("The front-matter must be a list of key: value fields.");
	}
	return document;
};

// Fields are read from the document's values, so an owned field set through
// an alias or a merge key reads as the server's decoder reads it. The
// editor's fields are single-line inputs, so line breaks in a value, such as
// a block scalar description, read as spaces.
const stringFields = (document: Document) => {
	let data: unknown;
	try {
		data = document.toJS();
	} catch (error) {
		throw new Error(
			`The front-matter is not valid YAML: ${error instanceof Error ? error.message : String(error)}`,
		);
	}
	const fields = new Map<string, unknown>(
		data && typeof data === "object" ? Object.entries(data) : [],
	);
	const read = (key: string) => {
		const value = fields.get(key);
		if (value === undefined || value === null) {
			return "";
		}
		if (typeof value !== "string") {
			throw new Error(`Front-matter field "${key}" must be a string.`);
		}
		return value.trim();
	};
	// An unchanged description is saved as written, so its full value must
	// fit, not only the one-line form the editor shows.
	const description = read("description");
	if (!isValidSkillDescription(description)) {
		throw new Error("The description must be 4096 bytes or smaller.");
	}
	return {
		name: read("name").replace(/\s*\n\s*/g, " "),
		description: description.replace(/\s*\n\s*/g, " "),
		model: read("model").replace(/\s*\n\s*/g, " "),
	};
};

// A replacement for an alias keeps the alias's comments.
const scalarFor = (value: unknown, replaced: Node) => {
	const scalar = new Scalar(value);
	scalar.comment = replaced.comment;
	scalar.commentBefore = replaced.commentBefore;
	scalar.spaceBefore = replaced.spaceBefore;
	return scalar;
};

/**
 * Throws when the content does not start with `---` front-matter. The
 * returned frontMatter is the original text, so buildAgentDefinitionMarkdown
 * keeps the keys and comments the editor does not own.
 */
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
		...stringFields(document),
		body: lines
			.slice(closingIndex + 1)
			.join("\n")
			.trim(),
		frontMatter,
	};
};

// A changed owned value can be an anchor that other keys alias. Copy the value
// into those aliases first, so changing or removing it leaves them as they
// were. Aliases of unchanged values stay aliases.
const inlineAliasesOf = (
	document: Document,
	keys: readonly (typeof ownedKeys)[number][],
) => {
	const owned = new Set(keys.map((key) => document.get(key, true)));
	visit(document, {
		Alias(_, alias) {
			const target = alias.resolve(document);
			if (!isScalar(target) || !owned.has(target)) {
				return undefined;
			}
			const copy = scalarFor(target.value, alias);
			copy.type = target.type;
			copy.tag = target.tag;
			return copy;
		},
	});
};

// The server's YAML decoder still reads some YAML 1.1 forms, such as dates,
// as other types, so a plain value must read back as the same string there.
// A value that does not convert at all, such as an unknown alias, needs
// quotes too.
const readsBackAsString = (value: string) => {
	const document = parseDocument(value, { version: "1.1" });
	try {
		return document.errors.length === 0 && document.toJS() === value;
	} catch {
		return false;
	}
};

/**
 * Rewrites only the name, description, and model in the front-matter, and
 * keeps every other key, comment, and alias.
 */
export const buildAgentDefinitionMarkdown = (
	values: AgentDefinitionFormValues,
): string => {
	const document = parseFrontMatter(values.frontMatter);
	const original = stringFields(document);
	const changed = ownedKeys.filter(
		(key) => values[key].trim() !== original[key],
	);
	inlineAliasesOf(document, changed);
	let inserted = 0;
	for (const key of ownedKeys) {
		const value = values[key].trim();
		const contents = document.contents;
		// An unchanged field keeps its node as written, such as a multiline
		// description the editor shows on one line.
		if (changed.includes(key)) {
			if (!value) {
				if (isMap(contents)) {
					contents.delete(key);
				}
			} else if (isMap(contents) && !contents.has(key)) {
				// A key the file lacks goes first, in the editor's order, as in
				// a new file.
				contents.items.splice(inserted++, 0, document.createPair(key, value));
			} else {
				// Setting an existing scalar keeps its node, so its style and
				// comments survive. On empty front-matter this creates the
				// mapping with this key first.
				const current = document.get(key, true);
				document.set(key, isAlias(current) ? scalarFor(value, current) : value);
				if (!isMap(contents)) {
					inserted++;
				}
			}
		}
		const node = document.get(key, true);
		if (
			isScalar(node) &&
			typeof node.value === "string" &&
			(node.type === undefined || node.type === Scalar.PLAIN) &&
			!readsBackAsString(node.value)
		) {
			node.type = Scalar.QUOTE_DOUBLE;
		}
	}
	// lineWidth 0 keeps long values on one line instead of folding them.
	return `---\n${document.toString({ lineWidth: 0 })}---\n${values.body.trim()}\n`;
};
