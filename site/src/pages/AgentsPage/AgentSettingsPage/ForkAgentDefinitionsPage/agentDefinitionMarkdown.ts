// This file is fork code: converts agent definition markdown to and from the
// editor's fields.

import {
	type Document,
	isAlias,
	isMap,
	isScalar,
	parseDocument,
	Scalar,
	visit,
} from "yaml";

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

// An owned field set through an alias reads as the value it aliases, as the
// server's decoder reads it. The editor's fields are single-line inputs, so
// line breaks in a value, such as a block scalar description, read as spaces.
const stringField = (document: Document, key: string) => {
	const node = document.get(key, true);
	const resolved = isAlias(node) ? node.resolve(document) : node;
	const value = isScalar(resolved) ? resolved.value : resolved;
	if (value === undefined || value === null) {
		return "";
	}
	if (typeof value !== "string") {
		throw new Error(`Front-matter field "${key}" must be a string.`);
	}
	return value.trim().replace(/\s*\n\s*/g, " ");
};

const stringFields = (document: Document) => ({
	name: stringField(document, "name"),
	description: stringField(document, "description"),
	model: stringField(document, "model"),
});

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
		...stringFields(document),
		body: lines
			.slice(closingIndex + 1)
			.join("\n")
			.trim(),
		frontMatter,
	};
};

// An owned value can be an anchor that other keys alias. Copy the value into
// those aliases first, so changing or removing it leaves them as they were.
const inlineOwnedAliases = (document: Document) => {
	const owned = new Set(ownedKeys.map((key) => document.get(key, true)));
	visit(document, {
		Alias(_, alias) {
			const target = alias.resolve(document);
			if (!isScalar(target) || !owned.has(target)) {
				return undefined;
			}
			const copy = new Scalar(target.value);
			copy.type = target.type;
			copy.comment = alias.comment;
			copy.commentBefore = alias.commentBefore;
			copy.spaceBefore = alias.spaceBefore;
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

/** Builds agent definition markdown from the editor's fields. */
export const buildAgentDefinitionMarkdown = (
	values: AgentDefinitionFormValues,
): string => {
	const document = parseFrontMatter(values.frontMatter);
	const original = stringFields(document);
	inlineOwnedAliases(document);
	let inserted = 0;
	for (const key of ownedKeys) {
		const value = values[key].trim();
		const contents = document.contents;
		// An unchanged field keeps its node as written, such as a multiline
		// description the editor shows on one line.
		if (value !== original[key]) {
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
				document.set(key, value);
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
