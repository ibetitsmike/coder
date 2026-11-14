// This file is fork code: the helpers of the fork-html-attachments viewer.

import type * as TypesGen from "#/api/typesGenerated";
import type { ParsedMessageEntry, RenderBlock } from "./types";

/** The `type` of the message a page posts to send text to the chat. */
export const forkHtmlMessageType = "coder:chat-message";

/** The longest text, in Unicode code points, that a page may send. */
export const forkHtmlMessageMaxLength = 4000;

/**
 * The shortest time between two messages from one viewer. Browsers keep a
 * click's activation for about this long, so one click sends at most one
 * message.
 */
export const forkHtmlMessageIntervalMs = 5000;

/**
 * Where the viewer loads a page from. The route serves it as text/html with a
 * policy that sandboxes it and blocks network access.
 */
export const getForkHtmlAttachmentURL = (fileId: string) =>
	`/api/v2/chats/fork/files/${encodeURIComponent(fileId)}/html`;

/**
 * Reports whether a file part is a stored HTML page. The server stores HTML
 * as text/plain, so the name decides, as it does on the server.
 */
export const isForkHtmlAttachment = (
	block: TypesGen.ChatFilePart,
): block is TypesGen.ChatFilePart & { file_id: string; name: string } =>
	Boolean(block.file_id) &&
	/\.html?$/i.test(block.name ?? "") &&
	(block.media_type === "text/plain" || block.media_type === "text/html");

/**
 * Returns the text of a message a page posted, or null unless it has exactly
 * the fields `type` and `text` and the trimmed text is 1 to
 * forkHtmlMessageMaxLength characters long.
 */
export const parseForkHtmlMessage = (data: unknown): string | null => {
	if (typeof data !== "object" || data === null || Array.isArray(data)) {
		return null;
	}
	const keys = Object.keys(data);
	if (keys.length !== 2 || !("type" in data) || !("text" in data)) {
		return null;
	}
	if (data.type !== forkHtmlMessageType || typeof data.text !== "string") {
		return null;
	}
	const text = data.text.trim();
	// A code point takes at most two UTF-16 units, so a longer string is
	// rejected before it is split into code points.
	if (text.length > forkHtmlMessageMaxLength * 2) {
		return null;
	}
	const length = [...text].length;
	if (length === 0 || length > forkHtmlMessageMaxLength) {
		return null;
	}
	return text;
};

/** The user message that carries a page's text and names the page. */
export const formatForkHtmlMessage = (name: string, text: string) =>
	`Message from the attached page "${name}":\n\n${text}`;

/**
 * Maps each HTML attachment name to the file ID of its newest attachment by
 * the agent, so an earlier page with the same name gives way to it. Pages in
 * user messages are uploads, not agent pages, and stay out. The live blocks
 * of the streaming turn come last, because they are the newest.
 */
export const latestForkHtmlAttachmentIds = (
	entries: readonly Pick<ParsedMessageEntry, "message">[],
	liveBlocks: readonly RenderBlock[] = [],
): ReadonlyMap<string, string> => {
	const latest = new Map<string, string>();
	const add = (part: RenderBlock | TypesGen.ChatMessagePart) => {
		if (part.type === "file" && isForkHtmlAttachment(part)) {
			latest.set(part.name, part.file_id);
		}
	};
	for (const { message } of entries) {
		if (message.role === "assistant") {
			for (const part of message.content ?? []) {
				add(part);
			}
		}
	}
	for (const block of liveBlocks) {
		add(block);
	}
	return latest;
};
